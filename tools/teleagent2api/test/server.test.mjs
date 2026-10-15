import test from "node:test";
import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "../src/server.mjs";

const adapterKey = "test-adapter-key-with-32-characters";
const accountKey = "ta-private-account-key";
const headers = { authorization: `Bearer ${accountKey}`, "x-sub2api-adapter-key": adapterKey, "content-type": "application/json" };
const body = JSON.stringify({ model: "chat-flash", messages: [{ role: "user", content: "OK" }], stream: true });

async function fixture(options = {}) {
  const login = { close: async () => {}, start: async (owner) => ({ session_id: "a".repeat(64), mode: "poll", status: "pending", owner }) };
  const server = createServer({
    store: { getByKey: (key) => key === accountKey ? { api_key: accountKey, uid: "u1", token: "secret-teleagent-token" } : undefined },
    login, adapterKey,
    forward: async () => new Response("data: {\"choices\":[]}\n\ndata: [DONE]\n\n", { headers: { "content-type": "text/event-stream" } }),
    ...options,
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return { server, url: `http://127.0.0.1:${server.address().port}` };
}

test("模型与推理必须同时通过部署密钥和独立账号密钥", async () => {
  const { server, url } = await fixture();
  try {
    for (const h of [{}, { authorization: `Bearer ${adapterKey}` }, { authorization: `Bearer ${accountKey}` }, { ...headers, authorization: "Bearer wrong-account" }]) {
      assert.equal((await fetch(url + "/v1/models", { headers: h })).status, 401);
    }
    const response = await fetch(url + "/v1/models", { headers });
    assert.equal(response.status, 200);
    const result = await response.json();
    assert.equal(result.data[0].id, "chat-pro");
    assert.equal(JSON.stringify(result).includes("secret-teleagent-token"), false);
  } finally { await server.stop(); }
});

test("SSE 原始字节保留，签名服务接收最终请求字节", async () => {
  let observed;
  const { server, url } = await fixture({ forward: async (account, bytes) => {
    observed = { account, bytes };
    return new Response("data: {\"choices\":[]}\n\ndata: [DONE]\n\n", { headers: { "content-type": "text/event-stream" } });
  } });
  try {
    const response = await fetch(url + "/v1/chat/completions", { method: "POST", headers, body });
    assert.equal(response.status, 200);
    assert.equal(await response.text(), "data: {\"choices\":[]}\n\ndata: [DONE]\n\n");
    assert.equal(observed.bytes.toString(), body);
    assert.equal(observed.account.uid, "u1");
  } finally { await server.stop(); }
});

test("上游错误状态保留且不会伪装为流成功", async () => {
  const { server, url } = await fixture({ forward: async () => Response.json({ code: 40104, message: "invalid signature" }, { status: 401 }) });
  try {
    const response = await fetch(url + "/v1/chat/completions", { method: "POST", headers, body });
    assert.equal(response.status, 401);
    assert.equal((await response.json()).code, 40104);
  } finally { await server.stop(); }
});

test("登录只允许内部密钥，必须提供账号归属", async () => {
  const { server, url } = await fixture();
  try {
    const endpoint = url + "/internal/login/sessions";
    assert.equal((await fetch(endpoint, { method: "POST", headers, body: "{}" })).status, 401);
    const auth = { authorization: `Bearer ${adapterKey}`, "content-type": "application/json" };
    assert.equal((await fetch(endpoint, { method: "POST", headers: auth, body: "{}" })).status, 403);
    const response = await fetch(endpoint, { method: "POST", headers: { ...auth, "x-login-owner": "user:4" }, body: "{}" });
    assert.equal(response.status, 200);
    assert.equal((await response.json()).owner, "user:4");
  } finally { await server.stop(); }
});

test("请求大小与模型在转发前校验", async () => {
  const { server, url } = await fixture({ maxBodyBytes: 128 });
  try {
    assert.equal((await fetch(url + "/v1/chat/completions", { method: "POST", headers, body: JSON.stringify({ model: "other", messages: [{}] }) })).status, 400);
    assert.equal((await fetch(url + "/v1/chat/completions", { method: "POST", headers, body: JSON.stringify({ messages: ["x".repeat(200)] }) })).status, 413);
  } finally { await server.stop(); }
});
