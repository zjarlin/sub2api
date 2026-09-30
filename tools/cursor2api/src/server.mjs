import http from "node:http";
import crypto from "node:crypto";
import { once } from "node:events";
import { abortable } from "./abort.mjs";
import { LoginSessions } from "./login.mjs";
import { CursorError, normalizeRequest, publicError } from "./request.mjs";

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
    req.on("aborted", () => reject(new CursorError(400, "request_aborted", "Request body was interrupted.")));
    req.on("end", () => {
      if (bytes > limit) {
        reject(new CursorError(413, "request_too_large", "Request body exceeds the Cursor adapter limit."));
        return;
      }
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
      } catch {
        reject(new CursorError(400, "invalid_json", "Request body must be valid JSON."));
      }
    });
  });
}

export function createServer({ runtime, sdk, adapterKey, timeoutMs = 300_000, maxConcurrent = 4, maxBodyBytes = 4 << 20 }) {
  if (!adapterKey) throw new Error("CURSOR_ADAPTER_KEY is required.");
  const logins = sdk ? new LoginSessions({ sdk }) : null;
  const active = new Set();
  let stopping = false;
  let stopPromise;
  const server = http.createServer(async (req, res) => {
    let timer;
    let heartbeat;
    let controller;
    let operation;
    const disconnected = () => {
      if (!res.writableEnded) controller?.abort(new CursorError(499, "client_disconnected", "Client disconnected."));
    };
    try {
      const path = new URL(req.url, "http://adapter.invalid").pathname;
      if (req.method === "GET" && path === "/livez") {
        return json(res, stopping ? 503 : 200, { status: stopping ? "stopping" : "ok" });
      }
      // 转发端点用 X-Sub2API-Adapter-Key；主服务的登录代理沿用 Authorization: Bearer。
      const presentedKey = req.headers["x-sub2api-adapter-key"]
        || String(req.headers.authorization || "").replace(/^Bearer\s+/i, "");
      if (!sameSecret(presentedKey, adapterKey)) {
        throw new CursorError(401, "invalid_adapter_key", "Missing or invalid adapter key.");
      }
      if (stopping) throw new CursorError(503, "adapter_shutdown", "Cursor adapter is shutting down.");
      if (req.method === "GET" && path === "/healthz") {
        return json(res, 200, { status: "ok", runtime: "cursor-sdk", upstream_verified: false });
      }
      if (path.startsWith("/internal/login/sessions")) {
        if (!logins) throw new CursorError(503, "login_unavailable", "Cursor login is unavailable in this adapter build.");
        res.setHeader("Cache-Control", "no-store");
        const owner = req.headers["x-login-owner"];
        if (typeof owner !== "string" || !owner.trim() || owner.length > 256) {
          throw new CursorError(400, "login_owner_required", "Login owner is required.");
        }
        if (req.method === "POST" && path === "/internal/login/sessions") {
          return json(res, 200, await logins.startResult(owner));
        }
        const match = /^\/internal\/login\/sessions\/([a-f0-9]{64})(\/poll)?$/.exec(path);
        if (match && req.method === "POST" && match[2]) {
          return json(res, 200, logins.poll(owner, match[1]));
        }
        if (match && req.method === "DELETE" && !match[2]) {
          logins.cancel(owner, match[1]);
          res.writeHead(204);
          return res.end();
        }
        throw new CursorError(404, "not_found", "Unknown login endpoint.");
      }
      if (!((req.method === "GET" && path === "/v1/models") || (req.method === "POST" && path === "/v1/chat/completions"))) {
        throw new CursorError(404, "not_found", "Endpoint not found.");
      }
      const authorization = req.headers.authorization;
      const apiKey = typeof authorization === "string" && authorization.startsWith("Bearer ") ? authorization.slice(7).trim() : "";
      if (!apiKey || apiKey.length > 4096) {
        throw new CursorError(401, "missing_cursor_key", "A Cursor account API key is required.");
      }
      if (active.size >= maxConcurrent) {
        throw new CursorError(503, "cursor_adapter_busy", "Cursor adapter concurrency limit reached.");
      }
      controller = new AbortController();
      active.add(controller);
      res.on("close", disconnected);
      timer = setTimeout(() => controller.abort(new CursorError(504, "cursor_timeout", "Cursor request timed out.")), timeoutMs);
      timer.unref();
      if (req.method === "GET") {
        operation = runtime.models(apiKey, { signal: controller.signal });
        const models = await abortable(operation, controller.signal);
        controller.signal.throwIfAborted();
        return json(res, 200, { object: "list", data: models });
      }
      if (!/^application\/json(?:\s*;|$)/i.test(req.headers["content-type"] || "")) {
        throw new CursorError(415, "invalid_content_type", "Content-Type must be application/json.");
      }
      if (req.headers["content-encoding"] && req.headers["content-encoding"] !== "identity") {
        throw new CursorError(415, "unsupported_encoding", "Compressed request bodies are not supported.");
      }
      if (Number(req.headers["content-length"]) > maxBodyBytes) {
        req.resume();
        throw new CursorError(413, "request_too_large", "Request body exceeds the Cursor adapter limit.");
      }
      const request = normalizeRequest(await abortable(readJSON(req, maxBodyBytes), controller.signal));
      controller.signal.throwIfAborted();
      const id = `chatcmpl-cursor-${crypto.randomUUID()}`;
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
        heartbeat.unref();
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
        if (!result.streamed) await writeSSE(chunk({ content: result.content }));
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
      }, { "X-Cursor-Usage-Source": result.usage ? "sdk" : "unavailable" });
    } catch (error) {
      const detail = publicError(controller?.signal.aborted ? controller.signal.reason : error);
      const type = detail.status === 429 ? "rate_limit_error" : detail.status >= 500 ? "upstream_error" : "invalid_request_error";
      const payload = { error: { type, code: detail.code, message: detail.message, status: detail.status } };
      if (res.destroyed) return;
      if (controller?.signal.aborted && !res.headersSent) res.setHeader("Connection", "close");
      if (res.headersSent) {
        // 流内错误不能附加 stop 或 [DONE]，由网关记录失败并保留部分输出。
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
    if (stopPromise) return stopPromise;
    stopPromise = (async () => {
    stopping = true;
    for (const controller of active) controller.abort(new CursorError(503, "adapter_shutdown", "Cursor adapter is shutting down."));
    server.closeIdleConnections();
    const closed = new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
    const forceClose = setTimeout(() => server.closeAllConnections(), 1000);
    forceClose.unref();
    try {
      await Promise.all([closed, runtime.stop?.(), logins?.close()]);
    } finally {
      clearTimeout(forceClose);
    }
    })();
    return stopPromise;
  };
  return server;
}
