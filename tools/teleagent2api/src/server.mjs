import http from "node:http";
import crypto from "node:crypto";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { AdapterError } from "./login.mjs";
import { forwardChat } from "./upstream.mjs";

const MODELS = ["chat-pro", "chat-flagship", "chat-flash", "chat-lite", "chat-nano"];

function sameKey(actual, expected) {
  if (typeof actual !== "string" || typeof expected !== "string") { return false; }
  const left = Buffer.from(actual);
  const right = Buffer.from(expected);
  return left.length === right.length && crypto.timingSafeEqual(left, right);
}

function json(response, status, value) {
  response.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" });
  response.end(JSON.stringify(value));
}

async function readBody(request, limit) {
  const chunks = [];
  let length = 0;
  for await (const chunk of request) {
    length += chunk.length;
    if (length > limit) { throw new AdapterError(413, "body_too_large", "Request body is too large."); }
    chunks.push(chunk);
  }
  const body = Buffer.concat(chunks);
  let value;
  try { value = JSON.parse(body.toString() || "{}"); } catch {
    throw new AdapterError(400, "invalid_json", "Request body must be JSON.");
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new AdapterError(400, "invalid_json", "Request body must be a JSON object.");
  }
  return { body, value };
}

export function createServer({ store, login, adapterKey, timeoutMs = 300_000, maxConcurrent = 4, maxBodyBytes = 4 << 20, forward = forwardChat }) {
  if (!adapterKey || adapterKey.length < 24) { throw new Error("TELEAGENT_ADAPTER_KEY must contain at least 24 characters."); }
  let active = 0;
  const activeAccounts = new Map();
  const controllers = new Set();
  const server = http.createServer((request, response) => {
    void handle(request, response).catch((error) => {
      if (response.headersSent) { response.destroy(); return; }
      const status = error instanceof AdapterError ? error.status : 502;
      const code = error instanceof AdapterError ? error.code : "teleagent_unavailable";
      const message = error instanceof AdapterError ? error.message : "TeleAgent request failed; retry or sign in again.";
      json(response, status, { error: { message, type: code, code }, message });
    });
  });
  server.requestTimeout = timeoutMs + 10_000;
  server.headersTimeout = 20_000;

  async function handle(request, response) {
    const url = new URL(request.url, "http://adapter.local");
    if (request.method === "GET" && url.pathname === "/livez") {
      json(response, 200, { status: "ok" });
      return;
    }
    const bearer = request.headers.authorization?.replace(/^Bearer /i, "");
    if (url.pathname.startsWith("/internal/login/")) {
      if (!sameKey(bearer, adapterKey)) { throw new AdapterError(401, "invalid_adapter_key", "Invalid adapter key."); }
      const owner = request.headers["x-login-owner"];
      if (typeof owner !== "string" || !owner || owner.length > 128) { throw new AdapterError(403, "invalid_owner", "Login owner is required."); }
      if (request.method === "POST" && url.pathname === "/internal/login/sessions") {
        await readBody(request, 64 << 10);
        json(response, 200, await login.start(owner));
        return;
      }
      const match = /^\/internal\/login\/sessions\/([a-f0-9]{64})(?:\/(poll|view|input))?$/.exec(url.pathname);
      if (!match) { throw new AdapterError(404, "not_found", "Login endpoint not found."); }
      const [, id, action] = match;
      if (request.method === "DELETE" && !action) {
        await login.cancel(owner, id);
        response.writeHead(204);
        response.end();
        return;
      }
      if (request.method === "POST" && action === "poll") {
        await readBody(request, 64 << 10);
        json(response, 200, login.poll(owner, id));
        return;
      }
      if (request.method === "GET" && action === "view") {
        const png = await login.view(owner, id);
        response.writeHead(200, { "Content-Type": "image/png", "Cache-Control": "no-store" });
        response.end(png);
        return;
      }
      if (request.method === "POST" && action === "input") {
        const { value } = await readBody(request, 64 << 10);
        await login.input(owner, id, value);
        json(response, 200, { status: "ok" });
        return;
      }
      throw new AdapterError(405, "method_not_allowed", "Unsupported login operation.");
    }
    if (!sameKey(request.headers["x-sub2api-adapter-key"], adapterKey)) {
      throw new AdapterError(401, "invalid_adapter_key", "Invalid adapter key.");
    }
    const account = store.getByKey(bearer);
    if (!account) { throw new AdapterError(401, "invalid_account_key", "Sign in to TeleAgent to add an account."); }
    if (request.method === "GET" && url.pathname === "/v1/models") {
      json(response, 200, { object: "list", data: MODELS.map((id) => ({ id, object: "model", created: 0, owned_by: "teleagent" })) });
      return;
    }
    if (request.method !== "POST" || url.pathname !== "/v1/chat/completions") {
      throw new AdapterError(404, "not_found", "Endpoint not found.");
    }
    if (active >= maxConcurrent || (activeAccounts.get(account.api_key) || 0) >= 2) {
      response.setHeader("Retry-After", "2");
      throw new AdapterError(429, "adapter_busy", "TeleAgent request capacity reached; retry shortly.");
    }
    const { body, value } = await readBody(request, maxBodyBytes);
    if (!MODELS.includes(value.model) || !Array.isArray(value.messages) || !value.messages.length) {
      throw new AdapterError(400, "invalid_request", "Select a TeleAgent model and include messages.");
    }
    // 读取请求体期间可能有其他请求占用名额，转发前再次同步检查。
    if (active >= maxConcurrent || (activeAccounts.get(account.api_key) || 0) >= 2) {
      response.setHeader("Retry-After", "2");
      throw new AdapterError(429, "adapter_busy", "TeleAgent request capacity reached; retry shortly.");
    }
    active++;
    activeAccounts.set(account.api_key, (activeAccounts.get(account.api_key) || 0) + 1);
    const controller = new AbortController();
    controllers.add(controller);
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    const disconnected = () => { if (!response.writableFinished) { controller.abort(); } };
    response.once("close", disconnected);
    try {
      const upstream = await forward(account, body, { signal: controller.signal });
      response.writeHead(upstream.status, {
        "Content-Type": upstream.headers.get("content-type") || "application/json",
        "Cache-Control": "no-store", "X-Accel-Buffering": "no",
        ...(upstream.headers.has("retry-after") ? { "Retry-After": upstream.headers.get("retry-after") } : {}),
      });
      if (upstream.body) { await pipeline(Readable.fromWeb(upstream.body), response); } else { response.end(); }
    } finally {
      clearTimeout(timer);
      response.off("close", disconnected);
      controllers.delete(controller);
      active--;
      activeAccounts.set(account.api_key, activeAccounts.get(account.api_key) - 1);
    }
  }

  server.stop = async () => {
    for (const controller of controllers) { controller.abort(); }
    server.closeIdleConnections();
    await Promise.all([new Promise((resolve) => server.close(resolve)), login.close()]);
  };
  return server;
}
