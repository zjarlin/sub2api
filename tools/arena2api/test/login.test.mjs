import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { LoginSessions } from "../src/login.mjs";
import { ArenaError } from "../src/request.mjs";
import { createServer } from "../src/server.mjs";

const input = { email: "owner@example.com", password: "private-password" };
const tick = () => new Promise((resolve) => setImmediate(resolve));

test("login returns only public state and isolates owners", async () => {
  const logins = new LoginSessions({ loginAndPrepare: async (email, password) => {
    assert.equal(password, input.password);
    return { uid: email, model_id: "arena-session-test" };
  } });
  const pending = logins.start("admin:1", input);
  assert.equal(pending.status, "pending");
  assert.match(pending.session_id, /^[a-f0-9]{64}$/);
  assert.throws(() => logins.poll("admin:2", pending.session_id), { status: 404 });
  assert.throws(() => logins.cancel("admin:2", pending.session_id), { status: 404 });
  await tick();
  const completed = logins.poll("admin:1", pending.session_id);
  assert.equal(completed.status, "completed");
  assert.equal(completed.account.model_id, "arena-session-test");
  assert.ok(!JSON.stringify(completed).includes(input.password));
  await logins.close();
});

test("cancellation retains the browser lock until work stops", async () => {
  let release;
  let signal;
  const logins = new LoginSessions({ loginAndPrepare: (_email, _password, current) => {
    signal = current;
    return new Promise((resolve) => { release = resolve; });
  } });
  const pending = logins.start("admin:1", input);
  logins.cancel("admin:1", pending.session_id);
  assert.equal(signal.aborted, true);
  assert.throws(() => logins.start("admin:1", input), { status: 429 });
  release({ uid: "discarded" });
  await tick();
  assert.equal(logins.active, null);
  assert.throws(() => logins.poll("admin:1", pending.session_id), { status: 404 });
  await logins.close();
});

test("failed login never returns upstream secrets and permits retry", async () => {
  const logins = new LoginSessions({ loginAndPrepare: async () => { throw new Error(input.password); } });
  const pending = logins.start("admin:1", input);
  await tick();
  assert.throws(() => logins.poll("admin:1", pending.session_id), (error) => error.code === "login_failed" && !error.message.includes(input.password));
  assert.equal(logins.start("admin:1", input).status, "pending");
  await logins.close();
});

test("failed login preserves only the whitelisted classification in status and diagnostics", async (t) => {
  const warn = t.mock.method(console, "warn", () => {});
  const error = new ArenaError(503, "arena_access_blocked", `${input.email} ${input.password} private-cookie`);
  error.stage = "arena_home";
  error.upstreamStatus = 403;
  const logins = new LoginSessions({ loginAndPrepare: async () => { throw error; } });
  const pending = logins.start("admin:1", input);
  await tick();
  assert.throws(() => logins.poll("admin:1", pending.session_id), { code: "arena_access_blocked", status: 503 });
  assert.deepEqual(JSON.parse(warn.mock.calls[0].arguments[0]), { event: "arena_login_failed", code: "arena_access_blocked", stage: "arena_home", upstream_status: 403 });
  assert.ok(!JSON.stringify(logins.sessions.get(pending.session_id)).includes(input.password));
  await logins.close();
});

test("expired login aborts work and is no longer pollable", async () => {
  let signal;
  const logins = new LoginSessions({ loginAndPrepare: (_email, _password, current) => {
    signal = current;
    return new Promise((_resolve, reject) => current.addEventListener("abort", () => reject(new Error("cancelled")), { once: true }));
  } });
  const pending = logins.start("admin:1", input);
  logins.sessions.get(pending.session_id).expires_at = 0;
  assert.throws(() => logins.poll("admin:1", pending.session_id), { status: 410 });
  assert.equal(signal.aborted, true);
  await logins.close();
});

test("HTTP login requires adapter auth and owner, supports poll and cancel", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "arena-login-"));
  const runtime = {
    loginAndPrepare: async () => ({ uid: input.email, model_id: "arena-test" }),
    close: async () => {},
  };
  const server = createServer({ config: { apiKey: "adapter-key", stateFile: path.join(directory, "state.json") }, runtime });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(async () => { await server.stop(); fs.rmSync(directory, { recursive: true, force: true }); });
  const url = `http://127.0.0.1:${server.address().port}/internal/login/sessions`;
  const headers = { Authorization: "Bearer adapter-key", "Content-Type": "application/json", "X-Login-Owner": "admin:1" };
  assert.equal((await fetch(url, { method: "POST" })).status, 401);
  assert.equal((await fetch(url, { method: "POST", headers: { Authorization: "Bearer adapter-key" } })).status, 400);
  assert.equal((await fetch(url, { method: "POST", headers, body: "{}" })).status, 400);
  const response = await fetch(url, { method: "POST", headers, body: JSON.stringify(input) });
  assert.equal(response.headers.get("cache-control"), "no-store");
  const pending = await response.json();
  const poll = `${url}/${pending.session_id}/poll`;
  assert.equal((await fetch(poll, { method: "POST", headers: { ...headers, "X-Login-Owner": "admin:2" } })).status, 404);
  const completed = await (await fetch(poll, { method: "POST", headers })).json();
  assert.equal(completed.status, "completed");
  assert.equal((await fetch(`${url}/${pending.session_id}`, { method: "DELETE", headers })).status, 204);
});

test("HTTP login exposes a static blocked-access reason and no upstream secrets", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "arena-login-blocked-"));
  t.mock.method(console, "warn", () => {});
  const runtime = {
    loginAndPrepare: async () => { throw new ArenaError(503, "arena_access_blocked", `${input.email} ${input.password} private-cookie`); },
    close: async () => {},
  };
  const server = createServer({ config: { apiKey: "adapter-key", stateFile: path.join(directory, "state.json") }, runtime });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(async () => { await server.stop(); fs.rmSync(directory, { recursive: true, force: true }); });
  const url = `http://127.0.0.1:${server.address().port}/internal/login/sessions`;
  const headers = { Authorization: "Bearer adapter-key", "Content-Type": "application/json", "X-Login-Owner": "admin:1" };
  const pending = await (await fetch(url, { method: "POST", headers, body: JSON.stringify(input) })).json();
  const response = await fetch(`${url}/${pending.session_id}/poll`, { method: "POST", headers });
  assert.equal(response.status, 503);
  const text = await response.text();
  assert.equal(JSON.parse(text).error.code, "arena_access_blocked");
  assert.ok(!text.includes(input.email) && !text.includes(input.password) && !text.includes("private-cookie"));
});
