import assert from 'node:assert/strict';
import { chmod, mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { AccountStore } from '../src/accounts.mjs';

const input = {
  uid: '123', owner: 'owner-1', nickname: '测试账号', token: 'test:header.payload.fake-signature',
  device_id: 'device-test', install_id: 'install-test', expires_at: '2026-11-15T00:00:00Z',
};

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'teleagent-accounts-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const dataDir = join(root, 'private');
  const store = new AccountStore({ dataDir });
  await store.load();
  return { store, dataDir };
}

test('生成 ta-256bit key，按 owner+uid 更新且重启后保持 key', async (t) => {
  const { store, dataDir } = await fixture(t);
  const first = await store.save(input);
  assert.match(first.api_key, /^ta-[a-f0-9]{64}$/u);
  assert.equal(first.token, input.token);
  assert.equal(store.getByKey('missing'), undefined);
  const second = await store.save({ ...input, nickname: '新昵称', token: 'test:updated.token.signature', api_key: 'ta-attacker' });
  assert.equal(second.api_key, first.api_key);
  assert.equal(store.all().length, 1);
  const reloaded = new AccountStore({ dataDir });
  await reloaded.load();
  assert.deepEqual(reloaded.getByKey(first.api_key), second);
  assert.equal((await reloaded.save({ ...input, uid: 123 })).api_key, first.api_key);
});

test('不同 owner 的同 uid 相互隔离，路径仅含哈希且分隔符不碰撞', async (t) => {
  const { store, dataDir } = await fixture(t);
  const accounts = [];
  for (const fields of [input, { ...input, owner: 'owner-2' }, { ...input, owner: 'a/b', uid: 'c' }, { ...input, owner: 'a', uid: 'b/c' }]) {
    accounts.push(await store.save(fields));
  }
  assert.equal(new Set(accounts.map((account) => account.api_key)).size, 4);
  const files = await readdir(dataDir);
  assert.equal(files.length, 4);
  for (const file of files) {
    assert.match(file, /^account-[a-f0-9]{64}\.json$/u);
  }
  assert.equal(store.getByKey(accounts[0].api_key).owner, 'owner-1');
  assert.equal(store.getByKey(accounts[1].api_key).owner, 'owner-2');
});

test('目录 0700、文件 0600，落盘完整且无临时文件，load 收紧已有权限', async (t) => {
  const { store, dataDir } = await fixture(t);
  const saved = await store.save(input);
  const [filename] = await readdir(dataDir);
  const file = join(dataDir, filename);
  assert.equal((await stat(dataDir)).mode & 0o777, 0o700);
  assert.equal((await stat(file)).mode & 0o777, 0o600);
  assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), saved);
  await chmod(dataDir, 0o755);
  await chmod(file, 0o644);
  await new AccountStore({ dataDir }).load();
  assert.equal((await stat(dataDir)).mode & 0o777, 0o700);
  assert.equal((await stat(file)).mode & 0o777, 0o600);
  assert.equal((await readdir(dataDir)).length, 1);
});

test('并发保存同一账号保持一个 key，读取结果不能改写内部状态', async (t) => {
  const { store } = await fixture(t);
  const saved = await Promise.all(Array.from({ length: 12 }, (_, index) => store.save({ ...input, nickname: `${index}` })));
  assert.equal(new Set(saved.map((account) => account.api_key)).size, 1);
  saved[0].token = 'changed';
  store.getByKey(saved[0].api_key).token = 'changed-again';
  store.all()[0].owner = 'changed-owner';
  assert.equal(store.all()[0].token, input.token);
  assert.equal(store.all()[0].owner, input.owner);
});

test('损坏文件和非法输入抛错且不回显 token，忽略福利状态文件', async (t) => {
  const { store, dataDir } = await fixture(t);
  await assert.rejects(store.save({ ...input, owner: '' }), /Invalid TeleAgent account owner/u);
  await assert.rejects(store.save({ ...input, token: `${input.token}\n` }), (error) => !error.message.includes(input.token));
  await store.save(input);
  await writeFile(join(dataDir, 'welfare.json'), '{"internal":true}');
  assert.equal((await new AccountStore({ dataDir }).load()).all().length, 1);
  const filename = (await readdir(dataDir)).find((file) => file.startsWith('account-'));
  await writeFile(join(dataDir, filename), `{invalid ${input.token}`);
  await assert.rejects(new AccountStore({ dataDir }).load(), (error) => {
    assert.match(error.message, /account file JSON/u);
    assert.equal(error.message.includes(input.token), false);
    return true;
  });
});

test('未加载不能覆盖已有 key，校验失败后仍可保存有效账号', async (t) => {
  const { store, dataDir } = await fixture(t);
  await assert.rejects(new AccountStore({ dataDir }).save(input), /Load TeleAgent/u);
  await assert.rejects(store.save({ ...input, install_id: '' }), /install_id/u);
  assert.match((await store.save(input)).api_key, /^ta-/u);
});
