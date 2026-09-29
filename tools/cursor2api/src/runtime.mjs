import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { Worker } from "node:worker_threads";
import { abortable } from "./abort.mjs";
import { CursorError, usageFromSDK } from "./request.mjs";

// `--input-type` only applies to eval/stdin entry points. Node inherits
// `process.execArgv` when creating a Worker, where the SDK worker is loaded
// from a file URL and the inherited flag makes startup fail.
export function workerExecArgv(execArgv = process.execArgv) {
  const filtered = [];
  for (let index = 0; index < execArgv.length; index += 1) {
    const argument = execArgv[index];
    if (argument === "--input-type") {
      index += 1;
      continue;
    }
    if (argument.startsWith("--input-type=")) continue;
    filtered.push(argument);
  }
  return filtered;
}

function createSDKWorker(data) {
  return new Worker(new URL("./sdk-worker.mjs", import.meta.url), {
    workerData: data,
    stdout: true,
    stderr: true,
    execArgv: workerExecArgv(),
  });
}

function variantID(id, params) {
  const values = params.map(({ id: key, value }) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).sort();
  return `${id}[${values.join(",")}]`;
}

export function modelEntries(catalog) {
  const entries = new Map();
  for (const model of catalog) {
    if (!model?.id || typeof model.id !== "string") continue;
    const base = {
      id: model.id,
      object: "model",
      created: 0,
      owned_by: "cursor",
      display_name: model.displayName || model.id,
      input_modalities: ["text"],
    };
    entries.set(model.id, { ...base, selection: { id: model.id } });
    for (const variant of model.variants || []) {
      if (!variant.params?.length) continue;
      const id = variantID(model.id, variant.params);
      entries.set(id, { ...base, id, display_name: variant.displayName || id, selection: { id: model.id, params: variant.params.map((p) => ({ ...p })) } });
    }
  }
  return entries;
}

export function selectModel(catalog, name, reasoningEffort) {
  const entry = modelEntries(catalog).get(name);
  if (!entry) {
    throw new CursorError(400, "model_not_found", "The requested model is absent from this Cursor account's model catalog.");
  }
  const definition = catalog.find((model) => model.id === entry.selection.id);
  const parameters = definition.parameters || [];
  const params = new Map((entry.selection.params || []).map(({ id, value }) => [id, value]));
  if (reasoningEffort !== undefined) {
    const parameter = reasoningEffort === "none"
      ? parameters.find((p) => p.id === "thinking" && p.values.some((v) => v.value === "false"))
      : parameters.find((p) => ["effort", "reasoning", "thought_level"].includes(p.id) && p.values.some((v) => v.value === reasoningEffort));
    if (!parameter) {
      throw new CursorError(400, "unsupported_reasoning_effort", "The selected Cursor model does not expose this reasoning effort.");
    }
    params.set(parameter.id, reasoningEffort === "none" ? "false" : reasoningEffort);
  }
  for (const [id, value] of params) {
    if (!parameters.some((p) => p.id === id && p.values.some((v) => v.value === value))) {
      throw new CursorError(502, "invalid_model_catalog", "Cursor returned an unsupported model parameter variant.");
    }
  }
  return { id: entry.selection.id, ...(params.size ? { params: [...params].map(([id, value]) => ({ id, value })) } : {}) };
}

export class CursorRuntime {
  constructor({ sdk, cacheTTL = 300_000, workerFactory = createSDKWorker } = {}) {
    this.sdk = sdk;
    this.cacheTTL = cacheTTL;
    this.catalogs = new Map();
    this.workerFactory = workerFactory;
    this.workers = new Set();
    this.directories = new Set();
    this.generations = new Set();
    this.stopping = false;
  }

  async catalog(apiKey, signal) {
    signal?.throwIfAborted();
    const key = createHash("sha256").update(apiKey).digest("hex");
    let item = this.catalogs.get(key);
    if (!item || (!item.pending && item.expires <= Date.now())) {
      // 模型目录按账号隔离，失败不缓存，也不回退到猜测的模型清单。
      item = { expires: Date.now() + this.cacheTTL, controller: new AbortController(), waiters: 0, pending: true };
      const query = this.sdk
        ? this.sdk.Cursor.models.list({ apiKey })
        : this.runWorker({ operation: "models", apiKey }, { signal: item.controller.signal });
      item.promise = query.then((catalog) => {
        if (!Array.isArray(catalog) || !modelEntries(catalog).size) {
          throw new CursorError(502, "empty_model_catalog", "Cursor returned no available models for this account.");
        }
        return catalog;
      }).catch((error) => {
        if (this.catalogs.get(key) === item) this.catalogs.delete(key);
        throw error;
      }).finally(() => { item.pending = false; });
      if (this.catalogs.size >= 64) this.catalogs.delete(this.catalogs.keys().next().value);
      this.catalogs.set(key, item);
    }
    item.waiters += 1;
    try {
      return await abortable(item.promise, signal);
    } finally {
      item.waiters -= 1;
      if (signal?.aborted && item.pending && !item.waiters) {
        if (this.catalogs.get(key) === item) this.catalogs.delete(key);
        item.controller.abort(signal.reason);
      }
    }
  }

  async models(apiKey, { signal } = {}) {
    const catalog = await this.catalog(apiKey, signal);
    return [...modelEntries(catalog).values()].map(({ selection, ...model }) => model);
  }

  generate(apiKey, request, options = {}) {
    const generation = this.generateRequest(apiKey, request, options);
    this.generations.add(generation);
    void generation.then(
      () => this.generations.delete(generation),
      () => this.generations.delete(generation),
    );
    return generation;
  }

  async generateRequest(apiKey, request, { signal, onText, onThinking } = {}) {
    const catalog = await this.catalog(apiKey, signal);
    signal?.throwIfAborted();
    if (this.stopping) throw new CursorError(503, "adapter_shutdown", "Cursor adapter is shutting down.");
    const model = selectModel(catalog, request.model, request.reasoningEffort);
    const directory = await mkdtemp(join(tmpdir(), "sub2api-cursor-"));
    this.directories.add(directory);
    try {
      if (this.sdk) {
        return await generateWithSDK(this.sdk, apiKey, request, model, directory, { signal, onText, onThinking });
      }
      return await this.runWorker({ operation: "generate", apiKey, request, model, directory }, { signal, onText, onThinking });
    } finally {
      await rm(directory, { recursive: true, force: true });
      this.directories.delete(directory);
    }
  }

  async runWorker(data, { signal, onText, onThinking } = {}) {
    signal?.throwIfAborted();
    if (this.stopping) throw new CursorError(503, "adapter_shutdown", "Cursor adapter is shutting down.");
    const worker = this.workerFactory(data);
    this.workers.add(worker);
    // SDK 诊断输出可能携带账号信息；只消费，不复制到适配器日志。
    worker.stdout?.resume();
    worker.stderr?.resume();
    try {
      return await abortable(new Promise((resolve, reject) => {
        worker.on("error", reject);
        worker.on("exit", () => reject(new CursorError(502, "cursor_worker_exited", "Cursor SDK worker exited without a result.")));
        worker.on("message", async (message) => {
          try {
            signal?.throwIfAborted();
            if (message.type === "result") resolve(message.value);
            else if (message.type === "error") reject(new CursorError(message.status, message.code, message.message));
            else if (message.type === "text" || message.type === "thinking") {
              await (message.type === "text" ? onText?.(message.value) : onThinking?.(message.value));
              worker.postMessage({ type: "ack", id: message.id });
            }
          } catch (error) {
            reject(error);
          }
        });
      }), signal);
    } finally {
      await worker.terminate();
      this.workers.delete(worker);
    }
  }

  async stop() {
    this.stopping = true;
    for (const item of this.catalogs.values()) item.controller.abort(new CursorError(503, "adapter_shutdown", "Cursor adapter is shutting down."));
    await Promise.allSettled([...this.workers].map((worker) => worker.terminate()));
    await Promise.allSettled([...this.generations]);
    await Promise.all([...this.directories].map((directory) => rm(directory, { recursive: true, force: true })));
  }
}

export async function generateWithSDK(sdk, apiKey, request, model, directory, { signal, onText, onThinking } = {}) {
    let agent;
    let run;
    let streamed = "";
    let reasoning = "";
    const cancel = () => {
      if (run) {
        void run.cancel().catch(() => agent?.close());
      } else {
        agent?.close();
      }
    };
    signal?.addEventListener("abort", cancel, { once: true });
    try {
      agent = await sdk.Agent.create({
        apiKey,
        model,
        tools: [],
        local: {
          cwd: directory,
          store: new sdk.JsonlLocalAgentStore(join(directory, "store")),
          settingSources: [],
          enableAgentRetries: false,
        },
      });
      signal?.throwIfAborted();
      run = await agent.send(request.prompt, {
        onDelta: async ({ update }) => {
          signal?.throwIfAborted();
          if (update.type === "text-delta") {
            streamed += update.text;
            await onText?.(update.text);
          } else if (update.type === "thinking-delta") {
            reasoning += update.text;
            await onThinking?.(update.text);
          }
        },
      });
      if (signal?.aborted) cancel();
      // 只从 onDelta 发送增量，stream() 中完整 assistant 消息不能重复输出。
      for await (const event of run.stream()) {
        signal?.throwIfAborted();
        if (event.type === "tool_call") {
          await run.cancel();
          throw new CursorError(502, "unexpected_cursor_tool", "Cursor attempted a tool call despite the disabled toolset.");
        }
      }
      const result = await run.wait();
      signal?.throwIfAborted();
      if (result.status !== "finished") {
        const error = new Error("Cursor run did not finish.");
        error.code = result.error?.code;
        throw error;
      }
      const content = result.result;
      if (typeof content !== "string" || !content.trim()) {
        throw new CursorError(502, "empty_cursor_response", "Cursor finished without a text answer.");
      }
      if (streamed && streamed !== content) {
        throw new CursorError(502, "inconsistent_cursor_stream", "Cursor's final answer differs from its streamed answer.");
      }
      return { content, reasoning, streamed: !!streamed, usage: usageFromSDK(result.usage), requestID: result.requestId };
    } finally {
      signal?.removeEventListener("abort", cancel);
      await agent?.[Symbol.asyncDispose]();
    }
}
