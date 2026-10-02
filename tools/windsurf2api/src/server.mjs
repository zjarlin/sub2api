import http from "node:http";
import crypto from "node:crypto";
import { once } from "node:events";
import { normalizeRequest, publicError } from "./request.mjs";
import { WindSurfError } from "./protocol.mjs";

function sameSecret(left, right) {
  if (typeof left !== "string" || !left || !right) return false;
  const a = crypto.createHash("sha256").update(left).digest();
  const b = crypto.createHash("sha256").update(right).digest();
  return crypto.timingSafeEqual(a, b);
}

function json(res, status, value, headers = {}) {
  res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store", ...headers });
  res.end(JSON.stringify(value));
}

function readJSON(req, limit) {
  return new Promise((resolve, reject) => {
    let bytes = 0;
    const chunks = [];
    req.on("data", (chunk) => {
      bytes += chunk.length;
      if (bytes <= limit) chunks.push(chunk);
    });
    req.on("error", reject);
    req.on("aborted", () => reject(new WindSurfError(400, "request_aborted", "Request body was interrupted.")));
    req.on("end", () => {
      if (bytes > limit) {
        reject(new WindSurfError(413, "request_too_large", "Request body exceeds the Windsurf adapter limit."));
        return;
      }
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
      } catch {
        reject(new WindSurfError(400, "invalid_json", "Request body must be valid JSON."));
      }
    });
  });
}

function abortable(promise, signal) {
  if (!signal) return promise;
  if (signal.aborted) return Promise.reject(signal.reason);
  return new Promise((resolve, reject) => {
    const onAbort = () => reject(signal.reason);
    signal.addEventListener("abort", onAbort, { once: true });
    Promise.resolve(promise).then(resolve, reject).finally(() => signal.removeEventListener("abort", onAbort));
  });
}

export function createServer({ runtime, adapterKey, timeoutMs = 300_000, maxConcurrent = 4, maxBodyBytes = 4 << 20 }) {
  if (!adapterKey) throw new Error("WINDSURF_ADAPTER_KEY is required.");
  const active = new Set();
  let stopping = false;
  const server = http.createServer(async (req, res) => {
    let timer;
    let heartbeat;
    let controller;
    let operation;
    const disconnected = () => {
      if (!res.writableEnded) controller?.abort(new WindSurfError(499, "client_disconnected", "Client disconnected."));
    };
    try {
      const path = new URL(req.url, "http://adapter.invalid").pathname;
      if (req.method === "GET" && path === "/livez") {
        return json(res, stopping ? 503 : 200, { status: stopping ? "stopping" : "ok" });
      }
      const presentedKey = req.headers["x-sub2api-adapter-key"]
        || String(req.headers.authorization || "").replace(/^Bearer\s+/i, "");
      if (!sameSecret(presentedKey, adapterKey)) {
        throw new WindSurfError(401, "invalid_adapter_key", "Missing or invalid adapter key.");
      }
      if (stopping) throw new WindSurfError(503, "adapter_shutdown", "Windsurf adapter is shutting down.");
      if (req.method === "GET" && path === "/healthz") {
        return json(res, 200, { status: "ok", runtime: "windsurf-connect", upstream_verified: false });
      }
      if (!((req.method === "GET" && path === "/v1/models") || (req.method === "POST" && path === "/v1/chat/completions"))) {
        throw new WindSurfError(404, "not_found", "Endpoint not found.");
      }
      const authorization = req.headers.authorization;
      const apiKey = typeof authorization === "string" && authorization.startsWith("Bearer ") ? authorization.slice(7).trim() : "";
      if (!apiKey || apiKey.length > 8192) {
        throw new WindSurfError(401, "missing_windsurf_key", "A Windsurf session token is required.");
      }
      if (active.size >= maxConcurrent) {
        throw new WindSurfError(503, "windsurf_adapter_busy", "Windsurf adapter concurrency limit reached.");
      }
      controller = new AbortController();
      active.add(controller);
      res.on("close", disconnected);
      timer = setTimeout(
        () => controller.abort(new WindSurfError(504, "windsurf_timeout", "Windsurf request timed out.")),
        timeoutMs,
      );
      timer.unref?.();

      if (req.method === "GET") {
        const models = await abortable(runtime.models(apiKey, controller.signal), controller.signal);
        return json(res, 200, {
          object: "list",
          data: models.map((model) => ({
            id: model.selector,
            object: "model",
            created: 0,
            owned_by: "windsurf",
            display_name: model.label || model.selector,
            input_modalities: ["text"],
          })),
        });
      }

      if (!/^application\/json(?:\s*;|$)/i.test(req.headers["content-type"] || "")) {
        throw new WindSurfError(415, "invalid_content_type", "Content-Type must be application/json.");
      }
      if (req.headers["content-encoding"] && req.headers["content-encoding"] !== "identity") {
        throw new WindSurfError(415, "unsupported_encoding", "Compressed request bodies are not supported.");
      }
      if (Number(req.headers["content-length"]) > maxBodyBytes) {
        req.resume();
        throw new WindSurfError(413, "request_too_large", "Request body exceeds the Windsurf adapter limit.");
      }
      const request = normalizeRequest(await abortable(readJSON(req, maxBodyBytes), controller.signal));
      controller.signal.throwIfAborted();

      const id = `chatcmpl-windsurf-${crypto.randomUUID()}`;
      const created = Math.floor(Date.now() / 1000);
      const chunk = (delta, finishReason = null) => ({
        id, object: "chat.completion.chunk", created, model: request.model,
        choices: [{ index: 0, delta, finish_reason: finishReason }],
      });
      const writeSSE = async (value) => {
        controller.signal.throwIfAborted();
        if (!res.write(`data: ${JSON.stringify(value)}\n\n`)) {
          await once(res, "drain", { signal: controller.signal });
        }
      };
      const beginStream = async () => {
        if (res.headersSent) return;
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache, no-transform", "X-Accel-Buffering": "no" });
        await writeSSE(chunk({ role: "assistant", content: "" }));
        heartbeat = setInterval(() => {
          if (!res.destroyed && !res.writableEnded) res.write(": keepalive\n\n");
        }, 10_000);
        heartbeat.unref?.();
      };
      operation = runtime.generate(apiKey, request, {
        signal: controller.signal,
        onText: request.stream ? async (text) => { await beginStream(); await writeSSE(chunk({ content: text })); } : undefined,
        onThinking: request.stream ? async (text) => { await beginStream(); await writeSSE(chunk({ reasoning_content: text })); } : undefined,
      });
      const result = await abortable(operation, controller.signal);
      controller.signal.throwIfAborted();
      if (request.stream) {
        await beginStream();
        await writeSSE(chunk({}, "stop"));
        if (request.includeUsage && result.usage) {
          await writeSSE({ id, object: "chat.completion.chunk", created, model: request.model, choices: [], usage: result.usage });
        }
        res.end("data: [DONE]\n\n");
        return;
      }
      const message = { role: "assistant", content: result.content };
      if (result.reasoning) message.reasoning_content = result.reasoning;
      return json(res, 200, {
        id, object: "chat.completion", created, model: request.model,
        choices: [{ index: 0, message, finish_reason: "stop" }],
        ...(result.usage ? { usage: result.usage } : {}),
      }, { "X-Windsurf-Usage-Source": result.usage ? "upstream" : "unavailable" });
    } catch (error) {
      const detail = publicError(controller?.signal.aborted ? controller.signal.reason : error);
      const type = detail.status === 429 ? "rate_limit_error" : detail.status >= 500 ? "upstream_error" : "invalid_request_error";
      const payload = { error: { type, code: detail.code, message: detail.message, status: detail.status } };
      if (res.destroyed) return;
      if (controller?.signal.aborted && !res.headersSent) res.setHeader("Connection", "close");
      if (res.headersSent) {
        res.end(`data: ${JSON.stringify(payload)}\n\n`);
        return;
      }
      return json(res, detail.status, payload, [429, 503].includes(detail.status) ? { "Retry-After": "30" } : {});
    } finally {
      clearTimeout(timer);
      clearInterval(heartbeat);
      res.off("close", disconnected);
      if (controller) {
        if (operation) operation.then(() => active.delete(controller), () => active.delete(controller));
        else active.delete(controller);
      }
    }
  });
  server.requestTimeout = 30_000;
  server.headersTimeout = 30_000;
  server.stop = () => {
    stopping = true;
    for (const controller of active) controller.abort(new WindSurfError(503, "adapter_shutdown", "Windsurf adapter is shutting down."));
    server.closeIdleConnections?.();
    const closed = new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())));
    const forceClose = setTimeout(() => server.closeAllConnections?.(), 1000);
    forceClose.unref?.();
    return Promise.all([closed, runtime.stop?.()]).finally(() => clearTimeout(forceClose));
  };
  return server;
}
