import { createHash, createHmac, randomUUID } from 'node:crypto';

const APP_VERSION = '2.7.0';
const SIGN_VERSION = 'superagent-auth-v1';
const MODELS = new Set(['chat-pro', 'chat-flagship', 'chat-flash', 'chat-lite', 'chat-nano']);

function requiredString(value, field) {
  if (typeof value !== 'string' || value.length === 0 || /[\r\n]/u.test(value)) {
    throw new TypeError(`Invalid TeleAgent ${field}`);
  }
  return value;
}

function bodyBytes(body) {
  if (typeof body === 'string' || body instanceof Uint8Array) {
    return Buffer.from(body);
  }
  if (body === undefined) {
    return Buffer.alloc(0);
  }
  try {
    return Buffer.from(JSON.stringify(body));
  } catch {
    throw new TypeError('TeleAgent body must be JSON serializable');
  }
}

export function signHeaders(account, { method = 'POST', path, body, timestamp, nonce } = {}) {
  const token = requiredString(account?.token, 'token');
  const installId = requiredString(account?.install_id, 'install_id');
  const deviceId = requiredString(account?.device_id, 'device_id');
  const requestPath = requiredString(path, 'path');
  const requestMethod = requiredString(method, 'method').toUpperCase();
  const requestNonce = requiredString(nonce ?? randomUUID(), 'nonce');
  const requestTimestamp = String(timestamp ?? Math.floor(Date.now() / 1000));
  if (!/^\d+$/u.test(requestTimestamp) || !requestPath.startsWith('/')) {
    throw new TypeError('Invalid TeleAgent timestamp or path');
  }
  const secret = token.split('.').at(-1);
  if (!secret) {
    throw new TypeError('Invalid TeleAgent token');
  }

  const innerMessage = [SIGN_VERSION, token, installId, requestTimestamp, requestNonce].join('/');
  const inner = createHmac('sha256', Buffer.from(secret, 'ascii')).update(innerMessage).digest('hex');
  const digest = createHash('sha256').update(bodyBytes(body)).digest('hex');
  const message = [SIGN_VERSION, requestMethod, requestPath, requestTimestamp, requestNonce, APP_VERSION, digest].join('\n');
  // 第二层使用十六进制文本本身作为密钥，不能解码为二进制。
  const signature = createHmac('sha256', Buffer.from(inner, 'ascii')).update(message).digest('hex');

  return {
    'content-type': 'application/json',
    accept: 'application/json, text/event-stream',
    'x-token': token,
    'x-superagent-device-id': deviceId,
    'x-superagent-install-id': installId,
    'x-superagent-nonce': requestNonce,
    'x-superagent-timestamp': requestTimestamp,
    'x-superagent-sign-version': 'v1',
    'x-superagent-signature': signature,
    'x-app-version': APP_VERSION,
    'x-device-os': 'linux',
    'x-device-os-version': 'linux',
    'x-route-target': 'ops-gateway',
    'x-teleai-client-type': 'desktop',
    'x-teleai-run-unit-type': '2',
    'x-request-id': randomUUID(),
    'x-message-id': randomUUID(),
    'x-session-id': randomUUID(),
    'x-teleai-query-id': randomUUID(),
    'x-teleai-upstream-request-id': randomUUID(),
    'x-teleai-root-session-id': randomUUID(),
  };
}

export async function forwardChat(account, body, {
  signal,
  fetchImpl = fetch,
  baseURL = 'https://agent.teleai.com.cn/superCowork/sapi',
} = {}) {
  const bytes = bodyBytes(body);
  let request;
  try {
    request = JSON.parse(bytes.toString('utf8'));
  } catch {
    throw new TypeError('TeleAgent chat body must be valid JSON');
  }
  if (!MODELS.has(request?.model)) {
    throw new TypeError('Unsupported TeleAgent chat model');
  }

  const url = new URL(`${baseURL.replace(/\/+$/u, '')}/api/v1/chat/completions`);
  const headers = signHeaders(account, { path: `${url.pathname}${url.search}`, body: bytes });
  try {
    return await fetchImpl(url.href, { method: 'POST', headers, body: bytes, signal, redirect: 'error' });
  } catch (error) {
    // 网络异常可能包含请求头；仅公开阶段和可安全识别的中止类型。
    const failure = new Error('TeleAgent chat upstream request failed');
    if (error?.name === 'AbortError' || error?.name === 'TimeoutError') {
      failure.name = error.name;
    }
    throw failure;
  }
}
