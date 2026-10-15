import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import test from 'node:test';
import { claimDaily, startWelfareSchedule } from '../src/welfare.mjs';

const account = {
  uid: '123', owner: 'owner-1', nickname: 'test', token: 'test:header.payload.fake-signature',
  device_id: 'test-device', install_id: 'test-install', api_key: 'ta-test-key',
};
const claimURL = 'https://agent.teleai.com.cn/superCowork/sapi/user/portal/companyStore/checkTask?type=daily_login';
const statusURL = 'https://agent.teleai.com.cn/superCowork/sapi/user/portal/companyStore/v2/checkDayTask';
const ok = (data = {}) => Response.json({ code: 200, data, msg: 'ok' });

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 2000; attempt += 1) {
    if (await predicate()) {
      return;
    }
    await delay(5);
  }
  assert.fail('异步签到未在测试期限内完成');
}

async function fixture(t, accounts = [account]) {
  const dataDir = await mkdtemp(join(tmpdir(), 'teleagent-welfare-test-'));
  t.after(() => rm(dataDir, { recursive: true, force: true }));
  return { dataDir, all: () => accounts };
}

test('每日签到和状态查询携带 token、版本、设备、秒时间戳、nonce，无 Authorization', async () => {
  const requests = [];
  const controller = new AbortController();
  const result = await claimDaily(account, {
    signal: controller.signal,
    fetchImpl: async (url, options) => {
      requests.push(url);
      assert.equal(options.method, 'GET');
      assert.ok(options.signal instanceof AbortSignal);
      assert.equal(options.signal.aborted, false);
      assert.equal(options.redirect, 'error');
      assert.equal(options.body, undefined);
      assert.equal(options.headers['x-token'], account.token);
      assert.equal(options.headers['x-app-version'], '2.7.0');
      assert.equal(options.headers['x-superagent-device-id'], account.device_id);
      assert.equal(options.headers['x-superagent-install-id'], account.install_id);
      assert.match(options.headers['x-timestamp'], /^\d{10}$/u);
      assert.match(options.headers['x-nonce'], /^[a-f0-9-]{36}$/u);
      assert.equal(options.headers.nonce, undefined);
      assert.equal(options.headers['x-superagent-signature'], undefined);
      assert.equal(options.headers.authorization, undefined);
      return ok({ checked: true, points: 10 });
    },
  });
  assert.deepEqual(requests, [claimURL, statusURL]);
  assert.equal(result.uid, account.uid);
  assert.equal(result.success, true);
  assert.equal(result.status, 'success');
  assert.match(result.message, /签到成功/u);
  assert.deepEqual(result.daily, { checked: true, points: 10 });
});

test('已签到 code200 仍成功，业务、HTTP、网络、解析和取消错误均抛出且不泄漏上游原文', async () => {
  assert.equal((await claimDaily(account, { fetchImpl: async () => ok({ already: true }) })).success, true);
  const failures = [
    async () => Response.json({ code: 401, msg: account.token }),
    async () => new Response(account.token, { status: 503 }),
    async () => new Response(`invalid ${account.token}`),
    async () => { throw new Error(`network ${account.token}`); },
    async () => { throw new DOMException(account.token, 'AbortError'); },
  ];
  for (const fetchImpl of failures) {
    await assert.rejects(claimDaily(account, { fetchImpl }), (error) => {
      assert.match(error.message, /TeleAgent/u);
      assert.equal(error.message.includes(account.token), false);
      assert.equal(error.cause, undefined);
      return true;
    });
  }
  let calls = 0;
  await assert.rejects(claimDaily(account, {
    fetchImpl: async () => { calls += 1; return calls === 1 ? ok() : new Response('', { status: 502 }); },
  }), /daily status failed \(HTTP 502\)/u);
  assert.equal(calls, 2);
});

test('两次福利请求分别使用 30 秒超时，中止和响应体超时均安全抛错', async (t) => {
  const controllers = [];
  t.mock.method(AbortSignal, 'timeout', (milliseconds) => {
    assert.equal(milliseconds, 30_000);
    const controller = new AbortController();
    controllers.push(controller);
    return controller.signal;
  });
  let calls = 0;
  const fetchImpl = async (_url, { signal }) => {
    calls += 1;
    if (calls === 1) {
      return ok();
    }
    return new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(signal.reason), { once: true });
    });
  };
  const pending = claimDaily(account, { fetchImpl });
  const rejected = assert.rejects(pending, (error) => {
    assert.equal(error.name, 'TimeoutError');
    assert.match(error.message, /daily status network request failed/u);
    assert.equal(error.message.includes(account.token), false);
    return true;
  });
  await waitFor(() => controllers.length === 2);
  controllers[1].abort(new DOMException(account.token, 'TimeoutError'));
  await rejected;
  assert.equal(calls, 2);

  const bodyPending = claimDaily(account, {
    fetchImpl: async (_url, { signal }) => ({
      ok: true,
      json: () => new Promise((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(signal.reason), { once: true });
      }),
    }),
  });
  const bodyRejected = assert.rejects(bodyPending, { name: 'TimeoutError', message: 'TeleAgent daily claim response was aborted' });
  await delay(5);
  controllers[2].abort(new DOMException(account.token, 'TimeoutError'));
  await bodyRejected;

  const cancelled = new AbortController();
  cancelled.abort(new Error(account.token));
  await assert.rejects(claimDaily(account, {
    signal: cancelled.signal, fetchImpl: () => assert.fail('已取消的请求不得发送'),
  }), (error) => !error.message.includes(account.token));
});

test('上海时区 00:10 执行，成功持久化、同日重启幂等、次日恢复执行', async (t) => {
  const store = await fixture(t);
  const results = [];
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => { calls += 1; return ok(); });
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: new Date('2026-10-14T16:09:00Z') });
  let stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  t.after(async () => stop());
  await delay(25);
  assert.equal(calls, 0);
  t.mock.timers.tick(60_000);
  await waitFor(() => results.length === 1);
  assert.equal(calls, 2);
  assert.equal(results[0].date, '2026-10-15');
  assert.equal(results[0].status, 'success');
  assert.equal(results[0].attempts, 1);
  const file = join(store.dataDir, 'welfare.json');
  const state = JSON.parse(await readFile(file, 'utf8'));
  const [entry] = Object.values(state.accounts);
  assert.deepEqual(entry, { date: '2026-10-15', status: 'success', attempts: 1, nextRetryAt: null });
  assert.equal((await stat(file)).mode & 0o777, 0o600);
  assert.equal((await stat(store.dataDir)).mode & 0o777, 0o700);
  const text = await readFile(file, 'utf8');
  assert.equal(text.includes(account.token), false);
  assert.equal(text.includes(account.api_key), false);
  await stop();
  stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  await delay(25);
  t.mock.timers.tick(60_000);
  await delay(25);
  assert.equal(calls, 2);
  t.mock.timers.setTime(new Date('2026-10-15T16:10:00Z').getTime());
  t.mock.timers.tick(60_000);
  await waitFor(() => results.length === 2);
  assert.equal(calls, 4);
  assert.equal(results[1].date, '2026-10-16');
});

test('重启补当日漏签，跨 owner 隔离，相同账号在途不重入且不阻塞其他账号', async (t) => {
  const other = { ...account, owner: 'owner-2', token: 'test:other.token.signature' };
  const store = await fixture(t, [account, other]);
  const results = [];
  let release;
  const pending = new Promise((resolve) => { release = resolve; });
  const counts = new Map();
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    const token = options.headers['x-token'];
    counts.set(token, (counts.get(token) ?? 0) + 1);
    if (token === account.token && url === claimURL) {
      await pending;
    }
    return ok({ raw: account.token });
  });
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: new Date('2026-10-15T05:00:00Z') });
  const stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  t.after(async () => { release(); await stop(); });
  await waitFor(() => results.length === 1);
  assert.equal(counts.get(account.token), 1);
  assert.equal(counts.get(other.token), 2);
  t.mock.timers.tick(60_000);
  await delay(25);
  assert.equal(counts.get(account.token), 1);
  release();
  await waitFor(() => results.length === 2);
  assert.equal(counts.get(account.token), 2);
  assert.equal(JSON.stringify(results).includes(account.token), false);
  const state = JSON.parse(await readFile(join(store.dataDir, 'welfare.json'), 'utf8'));
  assert.equal(Object.keys(state.accounts).length, 2);
});

test('失败采用 1/5/30 分钟重试，重启保持次数，当天至多四次且次日重置', async (t) => {
  const store = await fixture(t);
  const results = [];
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    calls += 1;
    throw new Error(`do not log ${account.token}`);
  });
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: new Date('2026-10-15T05:00:00Z') });
  let stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  t.after(async () => stop());
  await waitFor(() => results.length === 1);
  assert.equal(calls, 1);
  assert.equal(results[0].status, 'failed');
  assert.equal(JSON.stringify(results).includes(account.token), false);
  await stop();
  stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  await delay(25);
  assert.equal(calls, 1);
  t.mock.timers.tick(60_000);
  await waitFor(() => results.length === 2);
  assert.equal(calls, 2);
  t.mock.timers.tick(240_000);
  await delay(25);
  assert.equal(calls, 2);
  t.mock.timers.tick(60_000);
  await waitFor(() => results.length === 3);
  t.mock.timers.tick(1_800_000);
  await waitFor(() => results.length === 4);
  t.mock.timers.tick(3_600_000);
  await delay(25);
  assert.equal(calls, 4);
  assert.deepEqual(results.map((result) => result.attempts), [1, 2, 3, 4]);
  await stop();
  stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  await delay(25);
  assert.equal(calls, 4);
  t.mock.timers.setTime(new Date('2026-10-15T16:10:00Z').getTime());
  t.mock.timers.tick(60_000);
  await waitFor(() => results.length === 5);
  assert.equal(calls, 5);
  assert.equal(results[4].attempts, 1);
});

test('自定义时区按本地时间执行，stop 中止在途请求并停止后续调度', async (t) => {
  const store = await fixture(t);
  const results = [];
  let calls = 0;
  let receivedSignal;
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    calls += 1;
    receivedSignal = options.signal;
    return new Promise((_resolve, reject) => {
      options.signal.addEventListener('abort', () => reject(options.signal.reason), { once: true });
    });
  });
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: new Date('2026-10-15T00:09:00Z') });
  const stop = startWelfareSchedule(store, { timeZone: 'UTC', onResult: (result) => results.push(result) });
  t.after(async () => stop());
  await delay(25);
  assert.equal(calls, 0);
  t.mock.timers.tick(60_000);
  await waitFor(() => calls === 1);
  assert.equal(receivedSignal.aborted, false);
  await stop();
  assert.equal(receivedSignal.aborted, true);
  t.mock.timers.tick(86_400_000);
  await delay(25);
  assert.equal(calls, 1);
});

test('中断前的 pending 次数持久恢复，损坏状态可见报错且不触发签到', async (t) => {
  const store = await fixture(t);
  const id = createHash('sha256').update(JSON.stringify([account.owner, account.uid])).digest('hex');
  const now = new Date('2026-10-15T05:00:00Z').getTime();
  await writeFile(join(store.dataDir, 'welfare.json'), JSON.stringify({
    version: 1, accounts: { [id]: { date: '2026-10-15', status: 'pending', attempts: 1, nextRetryAt: now } },
  }));
  const results = [];
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => { calls += 1; return ok(); });
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now });
  const stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  t.after(async () => stop());
  await waitFor(() => results.length === 1);
  assert.equal(results[0].attempts, 2);
  await stop();
  await writeFile(join(store.dataDir, 'welfare.json'), `invalid ${account.token}`);
  const stopInvalid = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  await waitFor(() => results.length === 2);
  await stopInvalid();
  assert.equal(results[1].status, 'error');
  assert.equal(JSON.stringify(results).includes(account.token), false);
  assert.equal(calls, 2);
});

test('默认日志只包含 uid 和状态，校验时区和每日计划', async (t) => {
  const store = await fixture(t);
  const logs = [];
  t.mock.method(console, 'info', (...args) => logs.push(args));
  t.mock.method(globalThis, 'fetch', async () => ok({ raw: account.token }));
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: new Date('2026-10-15T05:00:00Z') });
  const stop = startWelfareSchedule(store);
  t.after(async () => stop());
  await waitFor(() => logs.length === 1);
  assert.deepEqual(logs, [['[teleagent welfare]', { uid: account.uid, status: 'success' }]]);
  assert.throws(() => startWelfareSchedule(store, { timeZone: 'Not/AZone' }), RangeError);
  assert.throws(() => startWelfareSchedule(store, { hour: 24 }), RangeError);
  assert.throws(() => startWelfareSchedule(store, { minute: -1 }), RangeError);
});

test('定时器中的同步异常和异步结果回调拒绝均可见报错且不产生未处理拒绝', async (t) => {
  const store = await fixture(t);
  const errors = [];
  const results = [];
  let throwFromStore = false;
  t.mock.method(store, 'all', () => {
    if (throwFromStore) {
      throw new Error(account.token);
    }
    return [account];
  });
  t.mock.method(console, 'error', (...args) => errors.push(args));
  t.mock.method(globalThis, 'fetch', async () => ok());
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: new Date('2026-10-15T05:00:00Z') });
  const stop = startWelfareSchedule(store, {
    onResult: async (result) => {
      results.push(result);
      throw new Error(`callback ${account.token}`);
    },
  });
  t.after(async () => stop());
  await waitFor(() => errors.length === 1);
  assert.equal(results[0].status, 'success');
  throwFromStore = true;
  t.mock.timers.tick(60_000);
  await waitFor(() => errors.length === 2);
  assert.equal(results[1].status, 'error');
  assert.match(results[1].message, /scheduler tick failed/u);
  assert.equal(JSON.stringify([errors, results]).includes(account.token), false);
  assert.deepEqual(errors[0], ['[teleagent welfare]', { uid: account.uid, status: 'callback_error' }]);
});

test('写盘失败在发请求前中断并报告，下一次重试恢复且 stop 等待全部任务退出', async (t) => {
  const store = await fixture(t, []);
  const results = [];
  let scans = 0;
  let accounts = [];
  t.mock.method(store, 'all', () => { scans += 1; return accounts; });
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => { calls += 1; return ok(); });
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: new Date('2026-10-15T05:00:00Z') });
  const stop = startWelfareSchedule(store, { onResult: (result) => results.push(result) });
  t.after(async () => stop());
  await waitFor(() => scans === 1);
  const file = join(store.dataDir, 'welfare.json');
  await mkdir(file);
  accounts = [account];
  t.mock.timers.tick(60_000);
  await waitFor(() => results.length === 1);
  assert.equal(results[0].status, 'error');
  assert.match(results[0].message, /persistence failed/u);
  assert.equal(calls, 0);
  await rm(file, { recursive: true });
  t.mock.timers.tick(60_000);
  await waitFor(() => results.length === 2);
  await stop();
  assert.equal(results[1].status, 'success');
  assert.equal(calls, 2);
  assert.equal(Object.values(JSON.parse(await readFile(file, 'utf8')).accounts)[0].status, 'success');
});
