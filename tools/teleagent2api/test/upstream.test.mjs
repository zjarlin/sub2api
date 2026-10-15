import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { forwardChat, signHeaders } from '../src/upstream.mjs';

const account = { token: 'test:header.payload.signature', install_id: 'test-install-001', device_id: 'test-device-001' };
const path = '/superCowork/sapi/api/v1/chat/completions';
const body = '{"model":"chat-pro","messages":[{"role":"user","content":"hello"}],"stream":true}';
const fixed = { path, body, timestamp: 1792023000, nonce: '12345678-1234-4234-9234-123456789abc' };

test('签名匹配独立 Python 与 OpenSSL 已知向量，保留完整 token 前缀', () => {
  // OpenSSL：正文摘要 b216fc6b…；内层 cf003ba0…；外层如下。
  const signature = '308baced633e0708ce5d9eea200abf74397bf8b65c2866a27770ecaf32a75778';
  const headers = signHeaders(account, fixed);
  assert.equal(headers['x-superagent-signature'], signature);
  assert.equal(signHeaders(account, fixed)['x-superagent-signature'], signature);
  assert.equal(headers['x-superagent-sign-version'], 'v1');
  assert.equal(headers['x-superagent-timestamp'], '1792023000');
  assert.equal(headers['x-superagent-nonce'], fixed.nonce);
  assert.equal(headers['x-token'], account.token);
  assert.equal(headers['x-app-version'], '2.7.0');
  assert.equal(headers['x-superagent-device-id'], account.device_id);
  assert.equal(headers['x-superagent-install-id'], account.install_id);
  assert.equal(headers['x-device-os'], 'linux');
  assert.equal(headers['x-device-os-version'], 'linux');
  assert.equal(headers['x-route-target'], 'ops-gateway');
  assert.equal(headers['x-teleai-client-type'], 'desktop');
  assert.equal(headers['x-teleai-run-unit-type'], '2');
  assert.equal(Object.keys(headers).some((key) => key.toLowerCase() === 'authorization'), false);
  for (const name of ['x-request-id', 'x-message-id', 'x-session-id', 'x-teleai-query-id', 'x-teleai-upstream-request-id', 'x-teleai-root-session-id']) {
    assert.match(headers[name], /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/u);
  }
});

test('正文、路径和 token 前缀各自改变签名，字符串与相同字节一致', () => {
  assert.equal(signHeaders(account, { ...fixed, body: `${body}\n` })['x-superagent-signature'],
    'b7cdfc2feb4e92a6cbb99dc58f3617a4a8b7587ad38c800db12f3542db48f6b8');
  assert.equal(signHeaders(account, { ...fixed, path: `${path}/changed` })['x-superagent-signature'],
    'b91682d8b75f093f89e81f4d67c8c15c96a4998d9589c5c37db718b117016f66');
  assert.equal(signHeaders({ ...account, token: 'changed:header.payload.signature' }, fixed)['x-superagent-signature'],
    'c3a90a2d59efed1d3fa2aa27c2448e4d2c18f7ff3c0e332c05078953de0a4cc4');
  assert.equal(signHeaders(account, { ...fixed, body: Buffer.from(body) })['x-superagent-signature'],
    signHeaders(account, fixed)['x-superagent-signature']);
  assert.notEqual(signHeaders(account, { ...fixed, method: 'GET' })['x-superagent-signature'],
    signHeaders(account, fixed)['x-superagent-signature']);
});

test('签名包含应用版本，并使用独立的另一版本已知向量验证', async () => {
  // 只在内存加载另一版本，生产接口始终固定 2.7.0。
  const source = await readFile(new URL('../src/upstream.mjs', import.meta.url), 'utf8');
  const changed = source.replace("const APP_VERSION = '2.7.0';", "const APP_VERSION = '2.7.1';");
  assert.notEqual(changed, source);
  const module = await import(`data:text/javascript;base64,${Buffer.from(changed).toString('base64')}`);
  const headers = module.signHeaders(account, fixed);
  assert.equal(headers['x-app-version'], '2.7.1');
  assert.equal(headers['x-superagent-signature'], 'ef0b54e5993c4960f5f8fa32f03c8e84b017ebf4339b1219bb5b33d4774e1d3b');
  assert.notEqual(headers['x-superagent-signature'], signHeaders(account, fixed)['x-superagent-signature']);
});

test('默认时间戳为秒且 nonce 为 UUID，参数错误不包含 token', () => {
  const before = Math.floor(Date.now() / 1000);
  const headers = signHeaders(account, { path, body });
  assert.ok(Number(headers['x-superagent-timestamp']) >= before);
  assert.ok(Number(headers['x-superagent-timestamp']) <= Math.floor(Date.now() / 1000));
  assert.match(headers['x-superagent-nonce'], /^[a-f0-9-]{36}$/u);
  assert.throws(() => signHeaders({ ...account, install_id: '' }, fixed), /Invalid TeleAgent install_id/u);
  assert.throws(() => signHeaders(account, { ...fixed, timestamp: 'bad' }), /Invalid TeleAgent timestamp/u);
});

test('转发原始 UTF-8 字节、tools、stream，签名对应最终正文且不读取流响应', async () => {
  const raw = ' {"model":"chat-flagship","messages":[{"role":"user","content":"你好"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"stream":true,"custom":{"keep":true}}\n';
  const controller = new AbortController();
  const response = new Response('data: {"ok":true}\n\n', { headers: { 'content-type': 'text/event-stream' } });
  let calls = 0;
  const result = await forwardChat(account, raw, {
    signal: controller.signal,
    fetchImpl: async (url, options) => {
      calls += 1;
      assert.equal(url, `https://agent.teleai.com.cn${path}`);
      assert.equal(options.method, 'POST');
      assert.equal(options.redirect, 'error');
      assert.equal(options.signal, controller.signal);
      assert.deepEqual(options.body, Buffer.from(raw));
      const expected = signHeaders(account, {
        path, body: options.body,
        nonce: options.headers['x-superagent-nonce'], timestamp: options.headers['x-superagent-timestamp'],
      });
      assert.equal(options.headers['x-superagent-signature'], expected['x-superagent-signature']);
      return response;
    },
  });
  assert.equal(result, response);
  assert.equal(response.bodyUsed, false);
  assert.equal(calls, 1);
});

test('仅支持五个真实模型，JSON 对象和字节正文均原样转发', async () => {
  for (const model of ['chat-pro', 'chat-flagship', 'chat-flash', 'chat-lite', 'chat-nano']) {
    const input = { model, stream: false, tools: [], messages: [], temperature: 0.123 };
    await forwardChat(account, input, {
      baseURL: 'http://localhost:12345/custom/',
      fetchImpl: async (url, options) => {
        assert.equal(url, 'http://localhost:12345/custom/api/v1/chat/completions');
        assert.deepEqual(JSON.parse(options.body.toString()), input);
        return new Response('{}');
      },
    });
  }
  const bytes = Buffer.from(body);
  await forwardChat(account, bytes, {
    fetchImpl: async (_url, options) => {
      assert.deepEqual(options.body, bytes);
      return new Response('{}');
    },
  });
  for (const input of [{ model: 'alias' }, { model: 'auto' }, '{invalid']) {
    await assert.rejects(forwardChat(account, input, { fetchImpl: () => assert.fail('不得请求上游') }), /TeleAgent/u);
  }
});

test('HTTP 错误原样返回且不重试；网络和中止异常不泄漏凭据', async () => {
  let calls = 0;
  const response = new Response('rate limited', { status: 429 });
  assert.equal(await forwardChat(account, body, {
    fetchImpl: async () => { calls += 1; return response; },
  }), response);
  assert.equal(calls, 1);
  await assert.rejects(forwardChat(account, body, {
    fetchImpl: async () => { throw new Error(`secret ${account.token}`); },
  }), (error) => {
    assert.equal(error.message.includes(account.token), false);
    assert.equal(error.cause, undefined);
    return true;
  });
  await assert.rejects(forwardChat(account, body, {
    fetchImpl: async () => { throw new DOMException(account.token, 'AbortError'); },
  }), { name: 'AbortError', message: 'TeleAgent chat upstream request failed' });
});
