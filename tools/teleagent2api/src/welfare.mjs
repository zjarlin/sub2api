import { createHash, randomUUID } from 'node:crypto';
import { chmod, mkdir, open, readFile, rename, unlink } from 'node:fs/promises';
import { join } from 'node:path';

const BASE_URL = 'https://agent.teleai.com.cn/superCowork/sapi';
const CLAIM_PATH = '/user/portal/companyStore/checkTask?type=daily_login';
const STATUS_PATH = '/user/portal/companyStore/v2/checkDayTask';
const RETRY_DELAYS = [60_000, 300_000, 1_800_000];
const MAX_ATTEMPTS = RETRY_DELAYS.length + 1;

async function requestTask(account, path, fetchImpl, signal, operation) {
  const url = new URL(`${BASE_URL}${path}`);
  for (const field of ['token', 'device_id', 'install_id']) {
    if (typeof account?.[field] !== 'string' || !account[field] || /[\r\n]/u.test(account[field])) {
      throw new TypeError(`Invalid TeleAgent welfare ${field}`);
    }
  }
  const headers = {
    accept: 'application/json',
    'x-token': account.token,
    'x-app-version': '2.7.0',
    'x-superagent-device-id': account.device_id,
    'x-superagent-install-id': account.install_id,
    'x-device-os': 'linux',
    'x-device-os-version': 'linux',
    'x-timestamp': String(Math.floor(Date.now() / 1000)),
    'x-nonce': randomUUID(),
  };
  const timeout = AbortSignal.timeout(30_000);
  const requestSignal = signal ? AbortSignal.any([signal, timeout]) : timeout;
  let response;
  try {
    requestSignal.throwIfAborted();
    response = await fetchImpl(url.href, { method: 'GET', headers, signal: requestSignal, redirect: 'error' });
  } catch (error) {
    const failure = new Error(`TeleAgent ${operation} network request failed`);
    if (error?.name === 'AbortError' || error?.name === 'TimeoutError') {
      failure.name = error.name;
    }
    throw failure;
  }
  if (!response.ok) {
    throw new Error(`TeleAgent ${operation} failed (HTTP ${response.status})`);
  }
  let payload;
  try {
    payload = await response.json();
  } catch {
    if (requestSignal.aborted) {
      const failure = new Error(`TeleAgent ${operation} response was aborted`);
      failure.name = requestSignal.reason?.name === 'TimeoutError' ? 'TimeoutError' : 'AbortError';
      throw failure;
    }
    throw new Error(`TeleAgent ${operation} returned invalid JSON`);
  }
  if (Number(payload?.code) !== 200) {
    throw new Error(`TeleAgent ${operation} returned an unsuccessful task code`);
  }
  return payload;
}

export async function claimDaily(account, { fetchImpl = fetch, signal } = {}) {
  const claim = await requestTask(account, CLAIM_PATH, fetchImpl, signal, 'daily claim');
  const daily = await requestTask(account, STATUS_PATH, fetchImpl, signal, 'daily status');
  return {
    uid: account.uid,
    success: true,
    status: 'success',
    message: '每日签到成功（含当日已签到）',
    claim: claim.data ?? null,
    daily: daily.data ?? null,
  };
}

function accountId(account) {
  return createHash('sha256').update(JSON.stringify([String(account.owner), String(account.uid)])).digest('hex');
}

async function readState(dataDir) {
  await mkdir(dataDir, { recursive: true, mode: 0o700 });
  await chmod(dataDir, 0o700);
  let text;
  try {
    text = await readFile(join(dataDir, 'welfare.json'), 'utf8');
  } catch (error) {
    if (error.code === 'ENOENT') {
      return { version: 1, accounts: {} };
    }
    throw error;
  }
  await chmod(join(dataDir, 'welfare.json'), 0o600);
  let state;
  try {
    state = JSON.parse(text);
  } catch {
    throw new Error('Invalid TeleAgent welfare state JSON');
  }
  if (state?.version !== 1 || !state.accounts || Array.isArray(state.accounts) || typeof state.accounts !== 'object') {
    throw new Error('Invalid TeleAgent welfare state');
  }
  for (const [id, entry] of Object.entries(state.accounts)) {
    if (!/^[a-f0-9]{64}$/u.test(id) || !/^\d{4}-\d{2}-\d{2}$/u.test(entry?.date)
      || !['pending', 'success', 'failed'].includes(entry.status)
      || !Number.isInteger(entry.attempts) || entry.attempts < 1 || entry.attempts > MAX_ATTEMPTS
      || (entry.nextRetryAt !== null && !Number.isFinite(entry.nextRetryAt))) {
      throw new Error('Invalid TeleAgent welfare account state');
    }
  }
  return state;
}

async function writeState(dataDir, text) {
  const temporary = join(dataDir, `.welfare.${randomUUID()}.tmp`);
  try {
    const file = await open(temporary, 'wx', 0o600);
    try {
      await file.chmod(0o600);
      await file.writeFile(text, 'utf8');
      await file.sync();
    } finally {
      await file.close();
    }
    await rename(temporary, join(dataDir, 'welfare.json'));
    const directory = await open(dataDir, 'r');
    try {
      await directory.sync();
    } finally {
      await directory.close();
    }
  } finally {
    try {
      await unlink(temporary);
    } catch (error) {
      if (error.code !== 'ENOENT') {
        throw error;
      }
    }
  }
}

export function startWelfareSchedule(store, {
  timeZone = 'Asia/Shanghai',
  hour = 0,
  minute = 10,
  onResult = ({ uid, status }) => console.info('[teleagent welfare]', { uid, status }),
} = {}) {
  if (!store?.dataDir || typeof store.all !== 'function') {
    throw new TypeError('Invalid TeleAgent welfare store');
  }
  if (!Number.isInteger(hour) || hour < 0 || hour > 23 || !Number.isInteger(minute) || minute < 0 || minute > 59) {
    throw new RangeError('Invalid TeleAgent welfare schedule time');
  }
  if (typeof onResult !== 'function') {
    throw new TypeError('Invalid TeleAgent welfare result callback');
  }
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  });
  const controller = new AbortController();
  const inFlight = new Set();
  const jobs = new Set();
  let stopped = false;
  let timer;
  let state;
  let writes = Promise.resolve();

  function report(result) {
    const callbackFailed = () => console.error('[teleagent welfare]', { uid: result.uid, status: 'callback_error' });
    try {
      Promise.resolve(onResult(result)).catch(callbackFailed);
    } catch {
      callbackFailed();
    }
  }

  function persist() {
    const text = `${JSON.stringify(state)}\n`;
    const result = writes.then(() => writeState(store.dataDir, text));
    // 每次写入的失败交回调用者，后续快照仍可串行落盘。
    writes = result.then(() => undefined, () => undefined);
    return result;
  }

  function localDay(now) {
    const parts = Object.fromEntries(formatter.formatToParts(now).map(({ type, value }) => [type, value]));
    return { date: `${parts.year}-${parts.month}-${parts.day}`, minute: Number(parts.hour) * 60 + Number(parts.minute) };
  }

  async function runAccount(account, id, date) {
    inFlight.add(id);
    const previous = state.accounts[id];
    const attempts = previous?.date === date ? previous.attempts + 1 : 1;
    const entry = {
      date, status: 'pending', attempts,
      nextRetryAt: attempts < MAX_ATTEMPTS ? Date.now() + RETRY_DELAYS[attempts - 1] : null,
    };
    state.accounts[id] = entry;
    try {
      // 先记录尝试次数，进程中断后仍受每日重试上限约束。
      await persist();
      if (stopped) {
        return;
      }
      let result;
      try {
        result = await claimDaily(account, { signal: controller.signal });
        entry.status = 'success';
        entry.nextRetryAt = null;
      } catch (error) {
        entry.status = 'failed';
        entry.nextRetryAt = attempts < MAX_ATTEMPTS ? Date.now() + RETRY_DELAYS[attempts - 1] : null;
        result = { uid: account.uid, success: false, status: 'failed', message: error.message };
      }
      await persist();
      report({ uid: account.uid, success: result.success, status: result.status, message: result.message, date, attempts });
    } finally {
      inFlight.delete(id);
    }
  }

  function tick() {
    if (stopped) {
      return;
    }
    try {
      const now = Date.now();
      const day = localDay(now);
      if (day.minute >= hour * 60 + minute) {
        launchDueAccounts(now, day);
      }
    } catch {
      report({ uid: null, status: 'error', message: 'TeleAgent welfare scheduler tick failed' });
    } finally {
      if (!stopped) {
        // 对齐分钟边界，跨日和夏令时均以配置的时区重新判断。
        timer = setTimeout(tick, 60_000 - (Date.now() % 60_000));
        timer.unref?.();
      }
    }
  }

  function launchDueAccounts(now, day) {
    for (const account of store.all()) {
      const id = accountId(account);
      const entry = state.accounts[id];
      const finished = entry?.date === day.date && (entry.status === 'success' || entry.attempts >= MAX_ATTEMPTS);
      const waiting = entry?.date === day.date && entry.nextRetryAt > now;
      if (inFlight.has(id) || finished || waiting) {
        continue;
      }
      const job = runAccount(account, id, day.date).catch(() => {
        report({ uid: account.uid, status: 'error', message: 'TeleAgent welfare persistence failed' });
      });
      jobs.add(job);
      job.then(() => jobs.delete(job));
    }
  }

  const initialization = readState(store.dataDir).then((loaded) => {
    state = loaded;
    tick();
  }).catch(() => {
    if (!stopped) {
      report({ uid: null, status: 'error', message: 'TeleAgent welfare state could not be loaded' });
    }
  });

  return function stop() {
    stopped = true;
    clearTimeout(timer);
    controller.abort();
    return initialization.then(async () => {
      await Promise.all(jobs);
      await writes;
    });
  };
}
