import assert from "node:assert/strict";
import { access } from "node:fs/promises";
import test from "node:test";
import { Worker } from "node:worker_threads";
import { CursorError } from "../src/request.mjs";
import { CursorRuntime, selectModel, workerExecArgv } from "../src/runtime.mjs";

const catalog = [{
  id: "composer-2.5",
  displayName: "Composer 2.5",
  parameters: [{ id: "effort", values: [{ value: "low" }, { value: "high" }] }],
  variants: [{ displayName: "Composer 2.5 High", params: [{ id: "effort", value: "high" }] }],
}];

test("removes input-type flags before starting file-based SDK workers", () => {
  assert.deepEqual(workerExecArgv([
    "--trace-warnings",
    "--input-type=module",
    "--input-type",
    "commonjs",
    "--no-warnings",
  ]), ["--trace-warnings", "--no-warnings"]);
});

test("exposes account catalog variants and validates reasoning against native parameters", async () => {
  const calls = [];
  const runtime = new CursorRuntime({ sdk: {
    Cursor: { models: { list: async (options) => { calls.push(options); return catalog; } } },
  } });
  const models = await runtime.models("account-key");
  assert.deepEqual(calls, [{ apiKey: "account-key" }]);
  assert.deepEqual(models.map((model) => model.id), ["composer-2.5", "composer-2.5[effort=high]"]);
  assert.deepEqual(selectModel(catalog, models[1].id), { id: "composer-2.5", params: [{ id: "effort", value: "high" }] });
  assert.deepEqual(selectModel(catalog, "composer-2.5", "low"), { id: "composer-2.5", params: [{ id: "effort", value: "low" }] });
  assert.throws(() => selectModel(catalog, "composer-2.5", "medium"), (error) => error.status === 400);
});

test("runs text in an isolated, tool-free SDK agent and requires a finished terminal", async () => {
  let options;
  let disposed = false;
  const runtime = new CursorRuntime({ sdk: {
    Cursor: { models: { list: async () => catalog } },
    JsonlLocalAgentStore: class { constructor(root) { this.root = root; } },
    Agent: { create: async (value) => {
      options = value;
      return {
        send: async (_prompt, { onDelta }) => {
          await onDelta({ update: { type: "text-delta", text: "Hello" } });
          return {
            stream: async function* () {},
            wait: async () => ({ status: "finished", result: "Hello", usage: {
              inputTokens: 7, outputTokens: 1, cacheReadTokens: 0, cacheWriteTokens: 0, totalTokens: 8,
            } }),
          };
        },
        [Symbol.asyncDispose]: async () => { disposed = true; },
      };
    } },
  } });
  const deltas = [];
  const result = await runtime.generate("account-key", { model: "composer-2.5", prompt: "Hello" }, {
    onText: (text) => deltas.push(text),
  });
  assert.deepEqual(deltas, ["Hello"]);
  assert.equal(result.content, "Hello");
  assert.equal(result.usage.total_tokens, 8);
  assert.equal(options.apiKey, "account-key");
  assert.deepEqual(options.tools, []);
  assert.deepEqual(options.local.settingSources, []);
  assert.equal(options.local.store.root.startsWith(options.local.cwd), true);
  assert.equal(disposed, true);
  await assert.rejects(access(options.local.cwd));
});

test("does not treat a failed SDK terminal as successful text", async () => {
  const runtime = new CursorRuntime({ sdk: {
    Cursor: { models: { list: async () => catalog } },
    JsonlLocalAgentStore: class {},
    Agent: { create: async () => ({
      send: async () => ({
        stream: async function* () {},
        wait: async () => ({ status: "error", result: "Partial", error: { code: "PRO_USER_USAGE_LIMIT", message: "Limit reached" } }),
      }),
      [Symbol.asyncDispose]: async () => {},
    }) },
  } });
  await assert.rejects(runtime.generate("account-key", { model: "composer-2.5", prompt: "Hello" }),
    (error) => error.code === "PRO_USER_USAGE_LIMIT");
});

test("cancelling a stalled SDK worker stops execution and removes its request directory", async () => {
  let generated;
  let started;
  const workerStarted = new Promise((resolve) => { started = resolve; });
  const runtime = new CursorRuntime({ workerFactory: (data) => {
    const worker = new Worker(`
      const { parentPort, workerData } = require("node:worker_threads");
      if (workerData.operation === "models") {
        parentPort.postMessage({ type: "result", value: [{ id: "composer-2.5" }] });
      } else {
        setInterval(() => {}, 1000);
      }
    `, { eval: true, workerData: data });
    if (data.operation === "generate") {
      generated = { worker, directory: data.directory };
      started();
    }
    return worker;
  } });
  const controller = new AbortController();
  const result = runtime.generate("account-key", { model: "composer-2.5", prompt: "Hello" }, { signal: controller.signal });
  await workerStarted;
  controller.abort(new CursorError(504, "cursor_timeout", "Cursor request timed out."));
  await assert.rejects(result, (error) => error.status === 504);
  assert.equal(generated.worker.threadId, -1);
  await assert.rejects(access(generated.directory));
  assert.equal(runtime.workers.size, 0);
  assert.equal(runtime.directories.size, 0);
});

test("runtime shutdown waits for stalled generation cleanup", async () => {
  let generated;
  let started;
  const workerStarted = new Promise((resolve) => { started = resolve; });
  const runtime = new CursorRuntime({ workerFactory: (data) => {
    const worker = new Worker(`
      const { parentPort, workerData } = require("node:worker_threads");
      if (workerData.operation === "models") {
        parentPort.postMessage({ type: "result", value: [{ id: "composer-2.5" }] });
      } else {
        setInterval(() => {}, 1000);
      }
    `, { eval: true, workerData: data });
    if (data.operation === "generate") {
      generated = data.directory;
      started();
    }
    return worker;
  } });
  const generation = runtime.generate("account-key", { model: "composer-2.5", prompt: "Hello" });
  await workerStarted;
  await runtime.stop();
  await assert.rejects(generation);
  await assert.rejects(access(generated));
  assert.equal(runtime.workers.size, 0);
  assert.equal(runtime.generations.size, 0);
});
