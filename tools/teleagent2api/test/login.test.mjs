import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { LoginSessions, tokenFromCallback, verifyToken } from "../src/login.mjs";

const token = `prefix:header.${Buffer.from(JSON.stringify({ exp: 1900000000 })).toString("base64url")}.signature`;
const userResponse = () => Response.json({ code: 200, data: { userId: "123", userName: "TeleAgent user" } });

function fixture(options = {}) {
  const wheels = [];
  const context = new EventEmitter();
  context.closed = false;
  context.close = async () => { context.closed = true; };
  context.newPage = async () => ({
    goto: async () => {}, screenshot: async () => Buffer.from("PNG"),
    mouse: { click: async () => {}, move: async () => {}, wheel: async (...delta) => { wheels.push(delta); } },
    keyboard: { insertText: async () => {}, press: async () => {} },
  });
  const saved = [];
  const store = { save: async (account) => { saved.push(account); return { ...account, api_key: "ta-test-account-key" }; } };
  const chromium = { launch: async () => ({ newContext: async () => context, close: async () => {} }) };
  const login = new LoginSessions({ chromium, store, fetchImpl: userResponse, ...options });
  return { login, context, saved, wheels };
}

test("只接收官方认证回调，拒绝同名路径及嵌入域名", () => {
  assert.equal(tokenFromCallback(`https://agent.teleai.com.cn/superCowork/sapi/cowork/auth?code=ok&token=${token}`), token);
  for (const url of [
    `https://evil.test/superCowork/sapi/cowork/auth?code=ok&token=${token}`,
    `https://agent.teleai.com.cn.evil.test/superCowork/sapi/cowork/auth?code=ok&token=${token}`,
    `https://agent.teleai.com.cn/superCowork/sapi/cowork/auth?token=${token}`,
  ]) { assert.equal(tokenFromCallback(url), null); }
});

test("账号身份由官方接口验证，令牌不写入对外返回值", async () => {
  const user = await verifyToken(token, { fetchImpl: userResponse });
  assert.deepEqual(user, { uid: "123", nickname: "TeleAgent user", expires_at: 1900000000 });
  await assert.rejects(verifyToken(token, { fetchImpl: async () => Response.json({ code: 401 }) }));
  const { login, context, saved } = fixture();
  try {
    const start = await login.start("user:4");
    assert.equal(start.mode, "poll");
    assert.equal(start.api_key, undefined);
    context.emit("request", { url: () => `https://agent.teleai.com.cn/superCowork/sapi/cowork/auth?code=ok&token=${token}` });
    await login.get("user:4", start.session_id).completing;
    const result = login.poll("user:4", start.session_id);
    assert.equal(result.status, "completed");
    assert.equal(result.api_key, "ta-test-account-key");
    assert.equal(saved.length, 1);
    assert.equal(saved[0].owner, "user:4");
    assert.equal(saved[0].token, token);
    assert.equal(JSON.stringify(result).includes(token), false);
    assert.equal(context.closed, true);
  } finally { await login.close(); }
});

test("会话隔离归属且到期清理浏览器", async () => {
  let now = 1000;
  const { login, context } = fixture({ now: () => now, ttlMs: 1000, maxSessions: 1 });
  try {
    const result = await login.start("admin:4");
    assert.throws(() => login.poll("user:4", result.session_id), { status: 404 });
    await assert.rejects(login.cancel("admin:5", result.session_id), { status: 404 });
    assert.equal((await login.start("admin:4")).session_id, result.session_id);
    assert.equal(login.sessions.size, 1);
    await assert.rejects(login.start("admin:5"), { status: 429 });
    now = 2001;
    assert.throws(() => login.poll("admin:4", result.session_id), { status: 410 });
    assert.equal(context.closed, true);
    assert.equal(login.sessions.size, 0);
  } finally { await login.close(); }
});

test("授权失败的会话仍可由归属用户取消并重新开始", async () => {
  const { login, context } = fixture({ fetchImpl: async () => Response.json({ code: 401 }) });
  try {
    const { session_id: id } = await login.start("user:4");
    context.emit("request", { url: () => `https://agent.teleai.com.cn/superCowork/sapi/cowork/auth?code=ok&token=${token}` });
    await login.sessions.get(id).completing;
    assert.throws(() => login.poll("user:4", id), { status: 502 });
    await assert.rejects(login.cancel("user:5", id), { status: 404 });
    await login.cancel("user:4", id);
    assert.equal(login.sessions.size, 0);
    assert.notEqual((await login.start("user:4")).session_id, id);
  } finally { await login.close(); }
});

test("登录交互不接受任意脚本或无界坐标", async () => {
  const { login } = fixture();
  try {
    const { session_id: id } = await login.start("user:4");
    await assert.rejects(login.input("user:4", id, { type: "click", x: -1, y: 0 }), { status: 400 });
    await assert.rejects(login.input("user:4", id, { type: "key", key: "Control+L" }), { status: 400 });
    await assert.rejects(login.input("user:4", id, { type: "script", text: "alert(1)" }), { status: 400 });
    assert.equal((await login.view("user:4", id)).toString(), "PNG");
  } finally { await login.close(); }
});

test("滚轮沿用 sub2api 的下划线字段并接受被省略的零值", async () => {
  const { login, wheels } = fixture();
  try {
    const { session_id: id } = await login.start("user:4");
    await login.input("user:4", id, { type: "wheel", x: 10, y: 20, delta_y: 120 });
    await login.input("user:4", id, { type: "wheel", x: 10, y: 20, delta_x: -4000, delta_y: 4000 });
    assert.deepEqual(wheels, [[0, 120], [-2000, 2000]]);
    await assert.rejects(login.input("user:4", id, { type: "wheel", x: 10, y: 20, delta_y: "120" }), { status: 400 });
  } finally { await login.close(); }
});
