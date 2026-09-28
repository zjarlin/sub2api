import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { ArenaError } from "../src/request.mjs";
import { createServer } from "../src/server.mjs";
import { adapterConfig, readModels } from "../src/runtime.mjs";
import { saveJSON } from "../src/state.mjs";

const SESSION = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const body = { model: "arena-session", messages: [{ role: "user", content: "hello" }] };

async function fixture(t, generate, options = {}) {
  const dataDir = options.dataDir || fs.mkdtempSync(path.join(os.tmpdir(), "arena2api-test-"));
  const calls = [];
  const config = { apiKey: "test-key", stateFile: path.join(dataDir, "clients.json"), timeoutMs: 1000, ...options };
  const runtime = {
    models: () => [{ id: "arena-session", sessionId: SESSION, accountEmail: "owner@example.com" }],
    health: () => ({ ready: options.ready !== false }),
    close: async () => {},
    generate: async (...args) => {
      calls.push(args);
      return generate ? generate(...args) : { choices: [{ message: { content: "answer" } }] };
    },
  };
  const server = createServer({ config, runtime });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  t.after(async () => {
    await server.stop();
    if (!options.dataDir) {
      fs.rmSync(dataDir, { recursive: true, force: true });
    }
  });
  const post = (request = body, headers = {}, signal) => fetch(`${origin}/v1/chat/completions`, { method: "POST", headers: { Authorization: "Bearer test-key", "Content-Type": "application/json", "X-Arena-Session-Id": "client-one", ...headers }, body: JSON.stringify(request), signal });
  return { origin, post, calls, server, dataDir };
}

test("empty service starts; liveness is public, account readiness is authenticated", async (t) => {
  const f = await fixture(t, null, { ready: false });
  assert.equal((await fetch(`${f.origin}/livez`)).status, 200);
  assert.equal((await fetch(`${f.origin}/v1/models`)).status, 401);
  assert.equal((await fetch(`${f.origin}/healthz`, { headers: { Authorization: "Bearer test-key" } })).status, 503);
  assert.equal((await f.post()).status, 503);
  assert.equal(f.calls.length, 0);
});

test("complete messages are forwarded and unknown usage is not fabricated", async (t) => {
  const f = await fixture(t);
  const request = { ...body, messages: [{ role: "system", content: "Chinese only" }, { role: "user", content: "old" }, { role: "assistant", content: "history" }, { role: "user", content: "current" }] };
  const response = await f.post(request);
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("x-arena-usage-source"), "unavailable");
  assert.equal((await response.json()).usage, undefined);
  for (const text of ["Chinese only", "history", "current"]) {
    assert.ok(f.calls[0][1].prompt.includes(text));
  }
});

test("tools, attachments and unsupported parameters fail before generation", async (t) => {
  const f = await fixture(t);
  const requests = [
    { ...body, tools: [{ type: "function", function: { name: "exec" } }] },
    { ...body, messages: [{ role: "user", content: [{ type: "text", text: "hi" }, { type: "image_url", image_url: { url: "https://example.com/image.png" } }] }] },
    { ...body, temperature: 0 },
    { ...body, logit_bias: { "123": 10 } },
    { ...body, custom_generation_control: true },
    { ...body, stream_options: { continuous_usage_stats: true } },
    { ...body, messages: [{ role: "tool", content: "result" }] },
    { ...body, messages: [{ role: "assistant", content: "history", reasoning_content: "reasoning" }, ...body.messages] },
  ];
  for (const request of requests) {
    assert.equal((await f.post(request)).status, 400);
  }
  assert.equal(f.calls.length, 0);
});

test("null generation controls and empty tools are accepted as absent", async (t) => {
  const f = await fixture(t);
  const response = await f.post({ ...body, tools: null, temperature: null, logit_bias: null, n: null });
  assert.equal(response.status, 200);
  assert.equal((await f.post({ ...body, tools: [] })).status, 200);
  assert.equal(f.calls.length, 2);
});

test("a session remains exclusive to its client after adapter restart", async (t) => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "arena2api-restart-"));
  t.after(() => fs.rmSync(dataDir, { recursive: true, force: true }));
  const first = await fixture(t, null, { dataDir });
  assert.equal((await first.post()).status, 200);
  await first.server.stop();
  const second = await fixture(t, null, { dataDir });
  const conflict = await second.post(body, { "X-Arena-Session-Id": "client-two" });
  assert.equal(conflict.status, 409);
  assert.equal((await conflict.json()).error.code, "session_already_bound");
  assert.equal(second.calls.length, 0);
});

test("named retries replay after restart; reusing the key for new input fails", async (t) => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "arena2api-replay-"));
  t.after(() => fs.rmSync(dataDir, { recursive: true, force: true }));
  const first = await fixture(t, null, { dataDir });
  const headers = { "Idempotency-Key": "turn-one" };
  const payload = await (await first.post(body, headers)).json();
  await first.server.stop();
  const second = await fixture(t, null, { dataDir });
  assert.deepEqual(await (await second.post(body, headers)).json(), payload);
  assert.equal(second.calls.length, 0);
  assert.equal((await second.post({ ...body, messages: [{ role: "user", content: "new" }] }, headers)).status, 409);
});

test("asking the same text again without an idempotency key starts a new turn", async (t) => {
  const f = await fixture(t);
  assert.equal((await f.post()).status, 200);
  assert.equal((await f.post()).status, 200);
  assert.equal(f.calls.length, 2);
});

test("streaming delivers real deltas, one terminal and no made-up usage", async (t) => {
  const f = await fixture(t, async (_model, _request, { onDelta }) => {
    onDelta("an");
    onDelta("swer");
    return { choices: [{ message: { content: "answer" } }] };
  });
  const response = await f.post({ ...body, stream: true, stream_options: { include_usage: true } });
  const text = await response.text();
  assert.ok(text.includes('"content":"an"'));
  assert.ok(text.includes('"content":"swer"'));
  assert.equal(text.match(/\[DONE\]/g).length, 1);
  assert.ok(text.includes('"finish_reason":"stop"'));
  assert.ok(!text.includes('"usage"'));
});

test("stream failures emit an error and cannot emit a successful terminal", async (t) => {
  const f = await fixture(t, async (_model, _request, { onDelta }) => {
    onDelta("partial");
    throw new ArenaError(502, "arena_stream_error", "upstream failed");
  });
  const text = await (await f.post({ ...body, stream: true })).text();
  assert.ok(text.includes('"code":"arena_stream_error"'));
  assert.ok(!text.includes('[DONE]'));
  assert.ok(!text.includes('"finish_reason":"stop"'));
  assert.equal((await f.post()).status, 409);
});

test("only one turn runs per instance", async (t) => {
  let release;
  let entered;
  const started = new Promise((resolve) => { entered = resolve; });
  const f = await fixture(t, async () => {
    entered();
    await new Promise((resolve) => { release = resolve; });
    return { choices: [{ message: { content: "answer" } }] };
  });
  const first = f.post();
  await started;
  assert.equal((await f.post()).status, 503);
  release();
  assert.equal((await first).status, 200);
  assert.equal(f.calls.length, 1);
});

test("timeout aborts the upstream and preserves the uncertain result", async (t) => {
  const f = await fixture(t, async (_model, _request, { signal }) => {
    await new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(signal.reason), { once: true }));
  }, { timeoutMs: 25 });
  assert.equal((await f.post()).status, 504);
  const response = await f.post();
  assert.equal(response.status, 409);
  assert.equal((await response.json()).error.code, "turn_outcome_unknown");
});

test("client disconnect reaches the upstream cancellation signal", async (t) => {
  let entered;
  let cancelled;
  const started = new Promise((resolve) => { entered = resolve; });
  const aborted = new Promise((resolve) => { cancelled = resolve; });
  const f = await fixture(t, async (_model, _request, { signal }) => {
    entered();
    return new Promise((_resolve, reject) => signal.addEventListener("abort", () => { cancelled(); reject(signal.reason); }, { once: true }));
  });
  const controller = new AbortController();
  const request = f.post(body, {}, controller.signal);
  await started;
  controller.abort();
  await assert.rejects(request);
  await aborted;
});

test("model configuration normalizes UUIDs; generated credential keys stay private", () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "arena2api-config-"));
  try {
    const config = adapterConfig({ DATA_DIR: dataDir });
    const again = adapterConfig({ DATA_DIR: dataDir });
    assert.equal(config.apiKey, again.apiKey);
    assert.equal(config.secret, again.secret);
    assert.equal(fs.statSync(path.join(dataDir, ".env")).mode & 0o777, 0o600);
    assert.deepEqual(readModels(config), []);
    saveJSON(config.modelsFile, [{ id: "arena-session", sessionId: SESSION.toUpperCase(), accountEmail: "owner@example.com" }]);
    assert.equal(readModels(config)[0].sessionId, SESSION);
  } finally {
    fs.rmSync(dataDir, { recursive: true, force: true });
  }
});
