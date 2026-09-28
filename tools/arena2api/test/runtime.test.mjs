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
  t.mock.method(ArenaBrowser.prototype, "getPage", async () => ({ evaluate: async () => "" }));
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
