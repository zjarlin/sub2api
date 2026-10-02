import assert from "node:assert/strict";
import { randomBytes, randomUUID } from "node:crypto";
import test from "node:test";
import {
  buildChatRequest, decodeCatalog, decodeFrame, normalizeUsage, WindSurfError,
} from "../src/protocol.mjs";
import { resolveModel } from "../src/runtime.mjs";
import { writeMessageField, writeStringField, writeVarintField, parseFields, getField } from "../src/proto.mjs";

const token = "devin-session-token$test";

test("builds a GetChatMessageRequest with system prompt, turns and selector", () => {
  const proto = buildChatRequest({
    token,
    model: "claude-opus-4-8-medium",
    messages: [
      { role: "system", content: "be brief" },
      { role: "user", content: "hello" },
      { role: "assistant", content: "hi" },
      { role: "user", content: "again" },
    ],
    completion: { maxTokens: 64, temperature: 0, topP: 0.5 },
  });
  const fields = parseFields(proto);
  assert.ok(getField(fields, 1, 2), "client metadata");
  assert.equal(getField(fields, 2, 2).value.toString("utf8"), "be brief");
  const turns = fields.filter((f) => f.field === 3 && f.wireType === 2);
  assert.equal(turns.length, 3);
  assert.equal(getField(fields, 21, 2).value.toString("utf8"), "claude-opus-4-8-medium");
  const completion = parseFields(getField(fields, 8, 2).value);
  assert.equal(getField(completion, 2, 0).value, 64);
  // temperature 0 clamps to the 0.001 epsilon floor
  assert.ok(Math.abs(getField(completion, 5, 1).value.readDoubleLE(0) - 0.001) < 1e-9);
});

test("decodes content, reasoning and usage from response frames", () => {
  const meta = Buffer.concat([writeVarintField(2, 100), writeVarintField(3, 5), writeVarintField(4, 7), writeVarintField(5, 30)]);
  const frame = Buffer.concat([
    writeStringField(3, "answer"),
    writeStringField(9, "thought"),
    writeMessageField(7, meta),
    writeVarintField(5, 2),
  ]);
  const decoded = decodeFrame(frame);
  assert.equal(decoded.contentBytes.toString("utf8"), "answer");
  assert.equal(decoded.reasoningBytes.toString("utf8"), "thought");
  assert.equal(decoded.finish, 2);
  assert.deepEqual(decoded.usage, {
    prompt_tokens: 130,
    completion_tokens: 5,
    total_tokens: 142,
    prompt_tokens_details: { cached_tokens: 30 },
    cache_creation_input_tokens: 7,
  });
});

test("decodes the model catalog with selector, alias and provider", () => {
  const modelInfo = writeStringField(23, "claude-opus-4.8");
  const entry = Buffer.concat([
    writeStringField(1, "Claude Opus 4.8 Medium"),
    writeStringField(22, "claude-opus-4-8-medium"),
    writeVarintField(10, 3),
    writeMessageField(23, modelInfo),
  ]);
  const disabled = Buffer.concat([writeStringField(22, "retired"), writeVarintField(4, 1)]);
  const raw = Buffer.concat([writeMessageField(1, entry), writeMessageField(1, disabled)]);
  assert.deepEqual(decodeCatalog(raw), [{
    selector: "claude-opus-4-8-medium",
    label: "Claude Opus 4.8 Medium",
    alias: "claude-opus-4.8",
    provider: 3,
  }]);
});

test("resolves a model by selector or alias and rejects unknown names", () => {
  const catalog = [{ selector: "claude-opus-4-8-medium", alias: "claude-opus-4.8" }];
  assert.equal(resolveModel(catalog, "claude-opus-4.8"), "claude-opus-4-8-medium");
  assert.equal(resolveModel(catalog, "claude-opus-4-8-medium"), "claude-opus-4-8-medium");
  assert.throws(() => resolveModel(catalog, "nope"), (error) => error instanceof WindSurfError && error.status === 400);
});

test("normalizes usage without inventing counters", () => {
  assert.equal(normalizeUsage({ completion: null }), null);
  assert.deepEqual(normalizeUsage({ prompt: 3, completion: 4 }), {
    prompt_tokens: 3, completion_tokens: 4, total_tokens: 7,
  });
});
