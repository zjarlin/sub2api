import assert from "node:assert/strict";
import test from "node:test";
import { normalizeRequest, publicError, usageFromSDK } from "../src/request.mjs";

const request = {
  model: "composer-2.5",
  messages: [
    { role: "system", content: "Answer concisely." },
    { role: "assistant", content: "Previous answer" },
    { role: "user", content: "Continue" },
  ],
};

test("normalizes complete text history without dropping roles", () => {
  const normalized = normalizeRequest(request);
  assert.equal(normalized.model, request.model);
  assert.deepEqual(JSON.parse(normalized.prompt.split("\n\n").at(-1)), request.messages);
  assert.equal(normalized.stream, false);
});

test("rejects unsupported caller tools, content and generation parameters", () => {
  for (const change of [
    { tools: [{ type: "function", function: { name: "lookup" } }] },
    { messages: [{ role: "user", content: [{ type: "image_url", image_url: { url: "https://example.com/a.png" } }] }] },
    { temperature: 0.5 },
    { max_tokens: 100 },
  ]) {
    assert.throws(() => normalizeRequest({ ...request, ...change }), (error) => error.status === 400);
  }
});

test("maps terminal SDK quota and authentication codes to their business status", () => {
  assert.equal(publicError({ code: "PRO_USER_RATE_LIMIT_EXCEEDED" }).status, 429);
  assert.equal(publicError({ code: "FREE_USER_USAGE_LIMIT" }).status, 429);
  assert.equal(publicError({ code: "BAD_USER_API_KEY" }).status, 401);
  assert.equal(publicError({ code: "MODEL_BLOCKED" }).status, 403);
});

test("maps SDK usage with cache tokens counted once", () => {
  assert.deepEqual(usageFromSDK({
    inputTokens: 10, cacheReadTokens: 3, cacheWriteTokens: 2,
    outputTokens: 4, reasoningTokens: 1, totalTokens: 19,
  }), {
    prompt_tokens: 15,
    completion_tokens: 4,
    total_tokens: 19,
    prompt_tokens_details: { cached_tokens: 3, cache_creation_tokens: 2 },
    completion_tokens_details: { reasoning_tokens: 1 },
  });
});
