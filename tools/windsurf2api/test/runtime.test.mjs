import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { WindSurfRuntime, resolveModel } from "../src/runtime.mjs";
import { wrapEnvelope } from "../src/connect.mjs";
import { writeMessageField, writeStringField, writeVarintField, getField, parseFields } from "../src/proto.mjs";

function fakeRequest(responseFactory) {
  return (options, callback) => {
    const req = new EventEmitter();
    req.end = () => {
      const res = responseFactory(options);
      const response = new EventEmitter();
      response.statusCode = res.statusCode;
      response.destroyed = false;
      process.nextTick(() => {
        callback(response);
        for (const chunk of res.chunks) response.emit("data", chunk);
        response.emit("end");
      });
    };
    req.destroy = () => {};
    return req;
  };
}

function catalogFrame() {
  const entry = Buffer.concat([
    writeStringField(1, "Claude Opus 4.8 Medium"),
    writeStringField(22, "claude-opus-4-8-medium"),
    writeVarintField(10, 3),
  ]);
  return Buffer.concat([writeMessageField(1, entry)]);
}

test("fetches and decodes the account model catalog", async () => {
  const requestImpl = fakeRequest(() => ({ statusCode: 200, chunks: [catalogFrame()] }));
  const runtime = new WindSurfRuntime({ requestImpl });
  const models = await runtime.models("devin-session-token$abc");
  assert.deepEqual(models.map((m) => m.selector), ["claude-opus-4-8-medium"]);
});

test("streams content, reasoning and usage over Connect frames", async () => {
  const meta = Buffer.concat([writeVarintField(2, 10), writeVarintField(3, 4)]);
  const dataFrame = wrapEnvelope(Buffer.concat([
    writeStringField(3, "Hello"),
    writeStringField(9, "thinking"),
    writeMessageField(7, meta),
  ]), { compress: false });
  const endFrame = wrapEnvelope(Buffer.from("{}"), { compress: false });
  endFrame[0] = 0x02;
  const requestImpl = fakeRequest((options) => {
    if (options.path.includes("GetCliModelConfigs")) {
      return { statusCode: 200, chunks: [catalogFrame()] };
    }
    return { statusCode: 200, chunks: [dataFrame, endFrame] };
  });
  const runtime = new WindSurfRuntime({ requestImpl });
  const updates = [];
  const result = await runtime.generate("devin-session-token$abc", {
    model: "claude-opus-4-8-medium",
    messages: [{ role: "user", content: "hi" }],
  }, { onText: (text) => updates.push(text) });
  assert.equal(result.content, "Hello");
  assert.equal(result.reasoning, "thinking");
  assert.equal(result.usage.prompt_tokens, 10);
  assert.equal(result.usage.completion_tokens, 4);
  assert.deepEqual(updates, ["Hello"]);
});

test("rejects an unknown model before calling the chat endpoint", async () => {
  let chatCalls = 0;
  const requestImpl = fakeRequest((options) => {
    if (options.path.includes("GetCliModelConfigs")) return { statusCode: 200, chunks: [catalogFrame()] };
    chatCalls += 1;
    return { statusCode: 200, chunks: [] };
  });
  const runtime = new WindSurfRuntime({ requestImpl });
  await assert.rejects(runtime.generate("tok", { model: "nope", messages: [{ role: "user", content: "hi" }] }), (error) => error.status === 400);
  assert.equal(chatCalls, 0);
});

test("rejects caller tools", async () => {
  const requestImpl = fakeRequest(() => ({ statusCode: 200, chunks: [catalogFrame()] }));
  const runtime = new WindSurfRuntime({ requestImpl });
  await assert.rejects(runtime.generate("tok", {
    model: "claude-opus-4-8-medium",
    messages: [{ role: "user", content: "hi" }],
    tools: [{ type: "function", function: { name: "x" } }],
  }), (error) => error.code === "unsupported_tools");
});
