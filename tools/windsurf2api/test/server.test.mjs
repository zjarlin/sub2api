import assert from "node:assert/strict";
import { once } from "node:events";
import test from "node:test";
import { createServer } from "../src/server.mjs";
import { WindSurfError } from "../src/protocol.mjs";

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

test("requires the deployment key and account token independently", async () => {
  const runtime = { models: async (key) => [{ selector: `${key}-model`, label: "M" }] };
  await withServer(runtime, async (url) => {
    const missingAdapter = await fetch(`${url}/v1/models`, { headers: { authorization: "Bearer account-key" } });
    assert.equal(missingAdapter.status, 401);
    const missingAccount = await fetch(`${url}/v1/models`, { headers: { "x-sub2api-adapter-key": "internal-key" } });
    assert.equal(missingAccount.status, 401);
    const accepted = await fetch(`${url}/v1/models`, {
      headers: { authorization: "Bearer account-key", "x-sub2api-adapter-key": "internal-key" },
    });
    assert.equal(accepted.status, 200);
    assert.deepEqual((await accepted.json()).data[0].id, "account-key-model");
  });
});

test("non-streaming chat returns an OpenAI completion", async () => {
  const runtime = {
    models: async () => [{ selector: "m", label: "M" }],
    generate: async () => ({ content: "Hi", reasoning: "", usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }),
  };
  await withServer(runtime, async (url) => {
    const response = await fetch(`${url}/v1/chat/completions`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: "Bearer account-key", "x-sub2api-adapter-key": "internal-key" },
      body: JSON.stringify({ model: "m", messages: [{ role: "user", content: "hi" }] }),
    });
    assert.equal(response.status, 200);
    const body = await response.json();
    assert.equal(body.choices[0].message.content, "Hi");
    assert.equal(body.usage.total_tokens, 2);
  });
});

test("SSE business failure emits an error without a success terminator", async () => {
  const runtime = {
    models: async () => [{ selector: "m", label: "M" }],
    generate: async (_key, _request, { onText }) => {
      await onText("partial");
      throw new WindSurfError(429, "windsurf_rate_limited", "Windsurf account rate or usage limit reached.");
    },
  };
  await withServer(runtime, async (url) => {
    const response = await fetch(`${url}/v1/chat/completions`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: "Bearer account-key", "x-sub2api-adapter-key": "internal-key" },
      body: JSON.stringify({ model: "m", messages: [{ role: "user", content: "hi" }], stream: true }),
    });
    assert.equal(response.status, 200);
    const body = await response.text();
    assert.match(body, /"content":"partial"/);
    assert.match(body, /"code":"windsurf_rate_limited"/);
    assert.doesNotMatch(body, /\[DONE\]|"finish_reason":"stop"/);
  });
});

test("exposes Windsurf OAuth2 login sessions behind the adapter key", async () => {
  const token = "devin-session-token$eyJhbGciOiJIUzI1NiJ9."
    + Buffer.from(JSON.stringify({ email: "user@example.com" })).toString("base64url") + ".sig";
  const runtime = {
    models: async () => [{ selector: "m", label: "M" }],
    userStatus: async () => ({ plan: "Pro", email: "user@example.com" }),
  };
  await withServer(runtime, async (url) => {
    const start = await fetch(`${url}/internal/login/sessions`, {
      method: "POST",
      headers: { "x-sub2api-adapter-key": "internal-key", "x-login-owner": "owner-1" },
    });
    assert.equal(start.status, 200);
    const session = await start.json();
    assert.equal(session.mode, "callback");
    assert.match(session.auth_url, /windsurf\.com\/windsurf\/signin/);

    const done = await fetch(`${url}/internal/login/sessions/${session.session_id}/callback`, {
      method: "POST",
      headers: { "content-type": "application/json", "x-sub2api-adapter-key": "internal-key", "x-login-owner": "owner-1" },
      body: JSON.stringify({ callback_url: `https://windsurf.com/show-auth-token?token=${encodeURIComponent(token)}` }),
    });
    assert.equal(done.status, 200);
    const result = await done.json();
    assert.equal(result.status, "completed");
    assert.equal(result.api_key, token);
  });
});

test("rejects tools with an invalid-request response", async () => {
  const runtime = { models: async () => [{ selector: "m", label: "M" }] };
  await withServer(runtime, async (url) => {
    const response = await fetch(`${url}/v1/chat/completions`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: "Bearer account-key", "x-sub2api-adapter-key": "internal-key" },
      body: JSON.stringify({ model: "m", messages: [{ role: "user", content: "hi" }], tools: [{ type: "function", function: { name: "x" } }] }),
    });
    assert.equal(response.status, 400);
    assert.equal((await response.json()).error.code, "unsupported_tools");
  });
});
