import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { chmod, mkdir, open, readFile, readdir, rename, unlink } from 'node:fs/promises';
import { join, resolve } from 'node:path';

function identity(value, field) {
  if ((typeof value !== 'string' && typeof value !== 'number') || String(value).trim() === '') {
    throw new TypeError(`Invalid TeleAgent account ${field}`);
  }
  if (typeof value === 'number' && !Number.isFinite(value)) {
    throw new TypeError(`Invalid TeleAgent account ${field}`);
  }
  return String(value);
}

function accountId(account) {
  return createHash('sha256').update(JSON.stringify([account.owner, account.uid])).digest('hex');
}

function accountFields(input) {
  const account = {
    uid: identity(input?.uid, 'uid'),
    nickname: input?.nickname ?? '',
    token: input?.token,
    device_id: input?.device_id,
    install_id: input?.install_id,
    expires_at: input?.expires_at ?? null,
    owner: identity(input?.owner, 'owner'),
  };
  for (const field of ['token', 'device_id', 'install_id']) {
    if (typeof account[field] !== 'string' || account[field].length === 0 || /[\r\n]/u.test(account[field])) {
      throw new TypeError(`Invalid TeleAgent account ${field}`);
    }
  }
  if (typeof account.nickname !== 'string') {
    throw new TypeError('Invalid TeleAgent account nickname');
  }
  if (account.expires_at !== null && typeof account.expires_at !== 'string' && !Number.isFinite(account.expires_at)) {
    throw new TypeError('Invalid TeleAgent account expires_at');
  }
  return account;
}

async function writeAccount(dataDir, filename, account) {
  const temporary = join(dataDir, `.${filename}.${randomUUID()}.tmp`);
  try {
    const file = await open(temporary, 'wx', 0o600);
    try {
      await file.chmod(0o600);
      await file.writeFile(`${JSON.stringify(account)}\n`, 'utf8');
      await file.sync();
    } finally {
      await file.close();
    }
    await rename(temporary, join(dataDir, filename));
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

export class AccountStore {
  #accounts = new Map();
  #keys = new Map();
  #queue = Promise.resolve();
  #loaded = false;

  constructor({ dataDir } = {}) {
    if (typeof dataDir !== 'string' || dataDir.length === 0) {
      throw new TypeError('TeleAgent dataDir is required');
    }
    this.dataDir = resolve(dataDir);
  }

  #enqueue(operation) {
    const result = this.#queue.then(operation);
    // 队列允许失败后的下一次操作；当前调用仍收到原始拒绝。
    this.#queue = result.then(() => undefined, () => undefined);
    return result;
  }

  async load() {
    return this.#enqueue(async () => {
      await mkdir(this.dataDir, { recursive: true, mode: 0o700 });
      await chmod(this.dataDir, 0o700);
      const accounts = new Map();
      const keys = new Map();
      const files = await readdir(this.dataDir, { withFileTypes: true });
      for (const file of files) {
        if (!/^account-[a-f0-9]{64}\.json$/u.test(file.name)) {
          continue;
        }
        if (!file.isFile()) {
          throw new Error('Invalid TeleAgent account file');
        }
        const filename = join(this.dataDir, file.name);
        await chmod(filename, 0o600);
        const text = await readFile(filename, 'utf8');
        let parsed;
        try {
          parsed = JSON.parse(text);
        } catch {
          throw new Error('Invalid TeleAgent account file JSON');
        }
        const account = accountFields(parsed);
        const id = accountId(account);
        if (file.name !== `account-${id}.json` || !/^ta-[a-f0-9]{64}$/u.test(parsed.api_key) || keys.has(parsed.api_key)) {
          throw new Error('Invalid TeleAgent account file identity or API key');
        }
        account.api_key = parsed.api_key;
        accounts.set(id, account);
        keys.set(account.api_key, account);
      }
      this.#accounts = accounts;
      this.#keys = keys;
      this.#loaded = true;
      return this;
    });
  }

  async save(input) {
    return this.#enqueue(async () => {
      if (!this.#loaded) {
        throw new Error('Load TeleAgent account store before saving');
      }
      const fields = accountFields(input);
      const id = accountId(fields);
      const existing = this.#accounts.get(id);
      const account = { ...fields, api_key: existing?.api_key ?? `ta-${randomBytes(32).toString('hex')}` };
      await writeAccount(this.dataDir, `account-${id}.json`, account);
      this.#accounts.set(id, account);
      this.#keys.set(account.api_key, account);
      return { ...account };
    });
  }

  getByKey(key) {
    const account = this.#keys.get(key);
    return account ? { ...account } : undefined;
  }

  all() {
    return [...this.#accounts.values()].map((account) => ({ ...account }));
  }
}
