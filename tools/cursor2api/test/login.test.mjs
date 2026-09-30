import assert from "node:assert/strict";
import { once } from "node:events";
import test from "node:test";
import { createServer } from "../src/server.mjs";

// 用一个可注入的假 SDK 复现 Cursor 官方登录流程，不访问任何真实上游。
function fakeSdk({ url = "https://cursor.com/loginDeepControl?challenge=x&uuid=y", apiKey = "minted-key" } = {}) {
  return {
    Cursor: {
      auth: {
        login: ({ onLoginUrl, signal, store }) => new Promise((resolve, reject) => {
          assert.equal(store, null, "login must not persist to disk");
          onLoginUrl(url);
          signal.addEventListener("abort", () => reject(Object.assign(new Error("aborted"), { name: "AuthenticationError" })), { once: true });
          setTimeout(() => resolve({ apiKey, email: "dev@example.com", apiKeyExpiresAtMs: Date.now() + 1000 }), 20);
        }),
      },
    },
  };
}

async function withServer(sdk, run) {
  const server = createServer({ runtime: { stop: async () => {} }, sdk, adapterKey: "internal-key" });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  try {
    return await run(`http://127.0.0.1:${server.address().port}`);
  } finally {
    await server.stop();
  }
}

test("cursor login returns the official URL and then the minted credential", async () => {
  await withServer(fakeSdk(), async (url) => {
    const headers = { authorization: "Bearer internal-key", "x-login-owner": "admin:1", "content-type": "application/json" };
    const started = await fetch(`${url}/internal/login/sessions`, { method: "POST", headers, body: "{}" });
    assert.equal(started.status, 200);
    const session = await started.json();
    assert.equal(session.mode, "poll");
    assert.match(session.auth_url, /loginDeepControl/);
    assert.equal(session.api_key, undefined, "key must not be exposed while pending");

    await new Promise((resolve) => setTimeout(resolve, 40));
    const polled = await fetch(`${url}/internal/login/sessions/${session.session_id}/poll`, { method: "POST", headers, body: "{}" });
    const done = await polled.json();
    assert.equal(done.status, "completed");
    assert.equal(done.api_key, "minted-key");
    assert.equal(done.account.email ?? done.account.nickname, "dev@example.com");
  });
});

test("cursor login isolates sessions by admin owner", async () => {
  await withServer(fakeSdk(), async (url) => {
    const base = { authorization: "Bearer internal-key", "content-type": "application/json" };
    const started = await fetch(`${url}/internal/login/sessions`, {
      method: "POST", headers: { ...base, "x-login-owner": "admin:1" }, body: "{}",
    });
    const session = await started.json();
    const foreign = await fetch(`${url}/internal/login/sessions/${session.session_id}/poll`, {
      method: "POST", headers: { ...base, "x-login-owner": "admin:2" }, body: "{}",
    });
    assert.equal(foreign.status, 404);
    const cancelled = await fetch(`${url}/internal/login/sessions/${session.session_id}`, {
      method: "DELETE", headers: { ...base, "x-login-owner": "admin:1" },
    });
    assert.equal(cancelled.status, 204);
  });
});
