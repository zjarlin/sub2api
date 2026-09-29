import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { Bridge } from "arena-local-bridge/src/bridge.mjs";
import { ArenaBrowser } from "arena-local-bridge/src/arena-login.mjs";
import { CredentialStore } from "arena-local-bridge/src/credentials.mjs";
import { ArenaError } from "../src/request.mjs";
import { adapterConfig, createRuntime } from "../src/runtime.mjs";

const model = { id: "arena-session", sessionId: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", accountEmail: "owner@example.com" };

function fixture(t) {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "arena2api-runtime-"));
  t.after(() => fs.rmSync(dataDir, { recursive: true, force: true }));
  t.mock.method(CredentialStore.prototype, "forSession", () => ({ email: model.accountEmail, cookieHeader: "", updatedAt: "now" }));
  t.mock.method(CredentialStore.prototype, "needsRefresh", () => false);
  const locator = {
    filter() { return this; },
    first() { return this; },
    last() { return this; },
    isVisible: async () => false,
    fill: async () => {},
    click: async () => {},
  };
  t.mock.method(ArenaBrowser.prototype, "getPage", async () => ({
    goto: async () => {},
    waitForFunction: async () => ({ jsonValue: async () => "ready", dispose: async () => {} }),
    getByRole: () => locator,
    locator: () => locator,
    waitForResponse: async () => ({ status: () => 200, text: async () => JSON.stringify({ id: model.sessionId }) }),
    evaluate: async () => ({ status: 200, html: JSON.stringify({ publicAccessToken: "private-agent-token" }) }),
  }));
  t.mock.method(ArenaBrowser.prototype, "launch", async () => ({
    newContext: async () => ({
      newPage: async () => ({ goto: async () => ({ status: () => 200 }), title: async () => "Arena" }),
      close: async () => {},
    }),
  }));
  return adapterConfig({ DATA_DIR: dataDir });
}

test("upstream browser recovery cannot submit the same turn twice", async (t) => {
  const config = fixture(t);
  const close = t.mock.method(ArenaBrowser.prototype, "close", async () => {});
  const append = t.mock.method(Bridge.prototype, "appendAgentMessage", async () => {
    throw new Error("Target page, context or browser has been closed");
  });
  const runtime = createRuntime(config);
  await assert.rejects(runtime.generate(model, { prompt: "hello" }, { signal: new AbortController().signal, idempotencyKey: "turn" }), { code: "arena_submission_uncertain" });
  assert.equal(append.mock.callCount(), 1);
  assert.equal(close.mock.callCount(), 1);
  await runtime.close();
});

test("cancellation during submission prevents browser recovery and closes the browser", async (t) => {
  const config = fixture(t);
  const controller = new AbortController();
  const close = t.mock.method(ArenaBrowser.prototype, "close", async () => {});
  const append = t.mock.method(Bridge.prototype, "appendAgentMessage", async () => {
    controller.abort(new ArenaError(504, "arena_timeout", "Generation timed out."));
    throw new Error("Target page, context or browser has been closed");
  });
  const runtime = createRuntime(config);
  await assert.rejects(runtime.generate(model, { prompt: "hello" }, { signal: controller.signal }), { code: "arena_timeout" });
  assert.equal(append.mock.callCount(), 1);
  assert.ok(close.mock.callCount() >= 1);
  await runtime.close();
});

// 新会话必须经过真实的登录与文本探测，成功前不能出现在目录中。
test("web login persists encrypted credentials and a new verified session", async (t) => {
  const config = fixture(t);
  t.mock.method(ArenaBrowser.prototype, "login", async () => ({ email: model.accountEmail, cookieHeader: "private-cookie", password: "private-password" }));
  t.mock.method(ArenaBrowser.prototype, "close", async () => {});
  const records = [
    { body: JSON.stringify({ data: { type: "text-delta", delta: "READY" } }) },
    { body: JSON.stringify({ data: { type: "finish", messageMetadata: { nodeId: "turn-1" } } }) },
  ];
  t.mock.method(Bridge.prototype, "readAgentOutput", async () => `data: ${JSON.stringify({ records })}\n\n`);
  const runtime = createRuntime(config);
  const result = await runtime.loginAndPrepare(model.accountEmail, "private-password", new AbortController().signal);
  assert.equal(result.model_id, `arena-session-${model.sessionId}`);
  assert.equal(runtime.models()[0].accountEmail, model.accountEmail);
  const saved = fs.readFileSync(config.core.credentialsFile, "utf8");
  assert.ok(!saved.includes("private-password"));
  assert.ok(!saved.includes("private-cookie"));
  assert.equal(JSON.stringify(result).includes("private"), false);
});

test("failed session probe does not publish a model or credentials", async (t) => {
  const config = fixture(t);
  const close = t.mock.method(ArenaBrowser.prototype, "close", async () => {});
  t.mock.method(ArenaBrowser.prototype, "login", async () => ({ email: model.accountEmail, cookieHeader: "cookie", password: "secret" }));
  t.mock.method(Bridge.prototype, "readAgentOutput", async () => "");
  const runtime = createRuntime(config);
  await assert.rejects(runtime.loginAndPrepare(model.accountEmail, "secret", new AbortController().signal), { code: "session_not_ready" });
  assert.deepEqual(runtime.models(), []);
  assert.equal(fs.existsSync(config.core.credentialsFile), false);
  assert.equal(close.mock.callCount(), 1);
});

test("Cloudflare stops web login before sign-in and cannot publish credentials or models", async (t) => {
  const config = fixture(t);
  t.mock.method(ArenaBrowser.prototype, "launch", async () => ({
    newContext: async () => ({
      newPage: async () => ({ goto: async () => ({ status: () => 403 }), title: async () => "Attention Required! | Cloudflare" }),
      close: async () => {},
    }),
  }));
  const signIn = t.mock.method(ArenaBrowser.prototype, "login", async () => { throw new Error("Sign-in must not run"); });
  t.mock.method(ArenaBrowser.prototype, "close", async () => {});
  const runtime = createRuntime(config);
  await assert.rejects(runtime.loginAndPrepare(model.accountEmail, "private-password", new AbortController().signal), { code: "arena_access_blocked", stage: "arena_home", upstreamStatus: 403 });
  assert.equal(signIn.mock.callCount(), 0);
  assert.equal(fs.existsSync(config.core.credentialsFile), false);
  assert.deepEqual(runtime.models(), []);
});

test("session creation failure has a safe preparation classification and publishes nothing", async (t) => {
  const config = fixture(t);
  t.mock.method(ArenaBrowser.prototype, "login", async () => ({ email: model.accountEmail, cookieHeader: "private-cookie", password: "private-password" }));
  t.mock.method(ArenaBrowser.prototype, "close", async () => {});
  t.mock.method(ArenaBrowser.prototype, "getPage", async () => { throw new Error("private-cookie private-password owner@example.com"); });
  const runtime = createRuntime(config);
  await assert.rejects(runtime.loginAndPrepare(model.accountEmail, "private-password", new AbortController().signal), (error) => {
    assert.equal(error.code, "arena_session_prepare_failed");
    assert.equal(error.stage, "agent_session");
    assert.ok(!error.message.includes("private") && !error.message.includes(model.accountEmail));
    return true;
  });
  assert.equal(fs.existsSync(config.core.credentialsFile), false);
  assert.deepEqual(runtime.models(), []);
});

test("cancelling web login before completion never saves credentials", async (t) => {
  const config = fixture(t);
  const controller = new AbortController();
  t.mock.method(ArenaBrowser.prototype, "close", async () => {});
  t.mock.method(ArenaBrowser.prototype, "login", async () => {
    controller.abort();
    return { email: model.accountEmail, cookieHeader: "cookie", password: "secret" };
  });
  const runtime = createRuntime(config);
  await assert.rejects(runtime.loginAndPrepare(model.accountEmail, "secret", controller.signal));
  assert.equal(fs.existsSync(config.core.credentialsFile), false);
  assert.deepEqual(runtime.models(), []);
});
