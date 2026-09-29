import crypto from "node:crypto";
import http from "node:http";
import { ArenaError, completion, identity, normalizeRequest, requestDigest, validateReply } from "./request.mjs";
import { LoginSessions } from "./login.mjs";
import { SessionState } from "./state.mjs";

function json(res, status, body, headers = {}) {
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(JSON.stringify(body));
}

function authenticated(req, key) {
  const supplied = Buffer.from(String(req.headers.authorization || "").replace(/^Bearer\s+/i, ""));
  const expected = Buffer.from(key);
  return supplied.length === expected.length && crypto.timingSafeEqual(supplied, expected);
}

async function readBody(req) {
  const chunks = [];
  let length = 0;
  for await (const chunk of req) {
    length += chunk.length;
    if (length > 1_000_000) {
      throw new ArenaError(413, "request_too_large", "Request exceeds 1 MB.");
    }
    chunks.push(chunk);
  }
  try {
    return JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } catch {
    throw new ArenaError(400, "invalid_json", "Request body must be valid JSON.");
  }
}

export function createServer({ config, runtime }) {
  const state = new SessionState(config.stateFile);
  const logins = new LoginSessions(runtime);
  let busy = false;
  let activeController = null;
  const server = http.createServer(async (req, res) => {
    let heartbeat;
    let timer;
    let controller;
    let ownsTurn = false;
    const disconnected = () => {
      if (!res.writableEnded) {
        controller?.abort(new ArenaError(499, "client_disconnected", "Client disconnected."));
      }
    };
    try {
      const pathname = new URL(req.url, "http://localhost").pathname.replace(/\/+$/, "") || "/";
      if (req.method === "GET" && pathname === "/livez") {
        return json(res, 200, { status: "ok", adapter: "arena" });
      }
      if (!authenticated(req, config.apiKey)) {
        throw new ArenaError(401, "invalid_api_key", "Invalid adapter key.");
      }
      if (pathname.startsWith("/internal/login/sessions")) {
        res.setHeader("Cache-Control", "no-store");
        const owner = req.headers["x-login-owner"];
        if (typeof owner !== "string" || !owner.trim() || owner.length > 256) {
          throw new ArenaError(400, "login_owner_required", "Login owner is required.");
        }
        if (req.method === "POST" && pathname === "/internal/login/sessions") {
          const input = await readBody(req);
          if (busy) {
            throw new ArenaError(429, "arena_busy", "Arena is generating. Retry after it finishes.");
          }
          return json(res, 200, logins.start(owner, input));
        }
        const match = /^\/internal\/login\/sessions\/([a-f0-9]{64})(\/poll)?$/.exec(pathname);
        if (match && req.method === "POST" && match[2]) {
          return json(res, 200, logins.poll(owner, match[1]));
        }
        if (match && req.method === "DELETE" && !match[2]) {
          logins.cancel(owner, match[1]);
          res.writeHead(204);
          return res.end();
        }
        throw new ArenaError(404, "not_found", "Unknown login endpoint.");
      }
      if (req.method === "GET" && pathname === "/healthz") {
        const health = runtime.health();
        return json(res, health.ready ? 200 : 503, { ...health, busy: busy || Boolean(logins.active) });
      }
      if (req.method === "GET" && pathname === "/v1/models") {
        const data = runtime.models().map((model) => ({ id: model.id, object: "model", created: 0, owned_by: "arena-session", name: model.name || model.id }));
        return json(res, 200, { object: "list", data });
      }
      if (req.method !== "POST" || pathname !== "/v1/chat/completions") {
        throw new ArenaError(404, "not_found", "Supported endpoints: GET /v1/models and POST /v1/chat/completions.");
      }
      const request = normalizeRequest(await readBody(req));
      const clientId = identity(req.headers["x-arena-session-id"] || req.headers["x-codex-session-id"] || req.headers["x-session-id"]);
      const suppliedKey = req.headers["x-arena-idempotency-key"] || req.headers["idempotency-key"];
      const key = suppliedKey ? crypto.createHash("sha256").update(identity(suppliedKey)).digest("hex") : "";
      const model = runtime.models().find((candidate) => candidate.id === request.model);
      if (!model) {
        throw new ArenaError(404, "model_not_found", "Configure a dedicated Arena session for this model first.");
      }
      if (busy || logins.active) {
        throw new ArenaError(503, "arena_busy", "The adapter is processing one turn. Retry after it completes.");
      }
      const health = runtime.health();
      if (!health.ready) {
        throw new ArenaError(503, "login_required", "Configure an Arena login and a dedicated session before generating.");
      }
      state.claim(model.sessionId, clientId);
      const cached = state.begin(model.sessionId, key, requestDigest(request));
      const responseHeaders = { "X-Arena-Usage-Source": "unavailable", "X-Request-Id": crypto.randomUUID() };
      const streamId = cached?.id || `chatcmpl-arena-${crypto.randomUUID()}`;
      const created = cached?.created || Math.floor(Date.now() / 1000);
      const writeChunk = (delta, finish = null) => {
        if (!res.destroyed && !res.writableEnded) {
          res.write(`data: ${JSON.stringify({ id: streamId, object: "chat.completion.chunk", created, model: request.model, choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`);
        }
      };
      if (request.stream) {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache, no-transform", "X-Accel-Buffering": "no", ...responseHeaders });
        writeChunk({ role: "assistant", content: "" });
        heartbeat = setInterval(() => writeChunk({}), 10_000);
        heartbeat.unref();
      }
      let payload = cached;
      let streamed = "";
      if (!payload) {
        busy = true;
        ownsTurn = true;
        controller = new AbortController();
        activeController = controller;
        res.on("close", disconnected);
        timer = setTimeout(() => controller.abort(new ArenaError(504, "arena_timeout", "Arena generation timed out.")), config.timeoutMs);
        timer.unref();
        const upstream = await runtime.generate(model, request, {
          signal: controller.signal,
          idempotencyKey: key,
          onDelta: request.stream ? (text) => {
            streamed += text;
            writeChunk({ content: text });
          } : null,
        });
        controller.signal.throwIfAborted();
        const content = validateReply(upstream);
        if (streamed && streamed.trim() !== content.trim()) {
          throw new ArenaError(502, "inconsistent_stream", "Arena's streamed answer differs from its final answer.");
        }
        payload = completion(request.model, content);
        payload.id = streamId;
        payload.created = created;
        state.finish(model.sessionId, key, payload);
      }
      if (request.stream) {
        if (!streamed) {
          writeChunk({ content: payload.choices[0].message.content });
        }
        writeChunk({}, "stop");
        res.end("data: [DONE]\n\n");
        return;
      }
      return json(res, 200, payload, responseHeaders);
    } catch (error) {
      const status = error instanceof ArenaError ? error.status : Number(error.status) || 502;
      const message = error instanceof ArenaError ? error.message : "Arena adapter request failed. Inspect the local account/session configuration.";
      const body = { error: { type: "arena_adapter_error", code: error.code || "arena_upstream_error", message } };
      if (res.destroyed) {
        return;
      }
      if (res.headersSent) {
        res.end(`data: ${JSON.stringify(body)}\n\n`);
        return;
      }
      return json(res, status, body, status === 503 ? { "Retry-After": "5" } : {});
    } finally {
      clearInterval(heartbeat);
      clearTimeout(timer);
      res.off("close", disconnected);
      if (ownsTurn) {
        busy = false;
        activeController = null;
      }
    }
  });
  server.requestTimeout = 30_000;
  server.headersTimeout = 30_000;
  server.stop = async () => {
    activeController?.abort(new ArenaError(503, "adapter_shutdown", "Arena adapter is shutting down."));
    server.closeIdleConnections();
    await logins.close();
    await runtime.close();
    await new Promise((resolve) => server.close(resolve));
  };
  return server;
}
