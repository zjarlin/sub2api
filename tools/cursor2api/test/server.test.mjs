import assert from "node:assert/strict";
import { once } from "node:events";
import test from "node:test";
import { CursorError } from "../src/request.mjs";
import { createServer } from "../src/server.mjs";

async function withServer(runtime, run, options = {}) {
  const server = createServer({ runtime, adapterKey: "internal-key", ...options });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  try {
    return await run(`http://127.0.0.1:${server.address().port}`);
  } finally {
    await server.stop();
  }
}

test("requires the deployment key and account key independently", async () => {
  const runtime = { models: async (key) => [{ id: `${key}-model` }] };
  await withServer(runtime, async (url) => {
    const missingAdapter = await fetch(`${url}/v1/models`, { headers: { authorization: "Bearer account-key" } });
    assert.equal(missingAdapter.status, 401);
    const missingAccount = await fetch(`${url}/v1/models`, { headers: { "x-sub2api-adapter-key": "internal-key" } });
    assert.equal(missingAccount.status, 401);
    const accepted = await fetch(`${url}/v1/models`, {
      headers: { authorization: "Bearer account-key", "x-sub2api-adapter-key": "internal-key" },
    });
    assert.equal(accepted.status, 200);
    assert.deepEqual((await accepted.json()).data, [{ id: "account-key-model" }]);
  });
});

test("a stalled model catalog returns a timeout response", async () => {
  const runtime = { models: () => new Promise(() => {}) };
  await withServer(runtime, async (url) => {
    const response = await fetch(`${url}/v1/models`, {
      headers: { authorization: "Bearer account-key", "x-sub2api-adapter-key": "internal-key" },
      signal: AbortSignal.timeout(2000),
    });
    assert.equal(response.status, 504);
    assert.equal((await response.json()).error.code, "cursor_timeout");
  }, { timeoutMs: 30 });
});

test("shutdown releases a pending model request and stops the runtime", async () => {
  let started;
  const modelStarted = new Promise((resolve) => { started = resolve; });
  let stopped = false;
  const runtime = {
    models: () => { started(); return new Promise(() => {}); },
    stop: async () => { stopped = true; },
  };
  const server = createServer({ runtime, adapterKey: "internal-key", timeoutMs: 10_000 });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const responsePromise = fetch(`http://127.0.0.1:${server.address().port}/v1/models`, {
    headers: { authorization: "Bearer account-key", "x-sub2api-adapter-key": "internal-key" },
    signal: AbortSignal.timeout(2000),
  });
  await modelStarted;
  const shutdown = server.stop();
  const response = await responsePromise;
  assert.equal(response.status, 503);
  assert.equal((await response.json()).error.code, "adapter_shutdown");
  await shutdown;
  assert.equal(stopped, true);
});

test("SSE business failure emits an error without a success terminator", async () => {
  const runtime = {
    generate: async (_key, _request, { onText }) => {
      await onText("partial");
      throw new CursorError(429, "rate_limit_exceeded", "Cursor account rate or usage limit reached.");
    },
  };
  await withServer(runtime, async (url) => {
    const response = await fetch(`${url}/v1/chat/completions`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer account-key",
        "x-sub2api-adapter-key": "internal-key",
      },
      body: JSON.stringify({ model: "composer-2.5", messages: [{ role: "user", content: "Hello" }], stream: true }),
    });
    assert.equal(response.status, 200);
    const body = await response.text();
    assert.match(body, /"content":"partial"/);
    assert.match(body, /"code":"rate_limit_exceeded"/);
    assert.doesNotMatch(body, /\[DONE\]|"finish_reason":"stop"/);
  });
});
