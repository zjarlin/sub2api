import https from "node:https";
import { randomBytes } from "node:crypto";
import { StringDecoder } from "node:string_decoder";
import { writeStringField, writeMessageField } from "./proto.mjs";
import { FrameParser, connectHeaders, wrapEnvelope } from "./connect.mjs";
import {
  WindSurfError, buildChatRequest, decodeCatalog, decodeFrame, classifyUpstreamError,
  modelFingerprint,
} from "./protocol.mjs";

const HOST = "server.codeium.com";
const CHAT_PATH = "/exa.api_server_pb.ApiServerService/GetChatMessage";
const CATALOG_PATH = "/exa.api_server_pb.ApiServerService/GetCliModelConfigs";

function authHeader(token) {
  // The upstream requires the session token doubled, dash-joined.
  return `Basic ${token}-${token}`;
}

function withTimeout(promise, ms, controller, onTimeout) {
  const timer = setTimeout(() => { onTimeout?.(); controller.abort(new WindSurfError(504, "windsurf_timeout", "Windsurf request timed out.")); }, ms);
  timer.unref?.();
  return promise.finally(() => clearTimeout(timer));
}

export class WindSurfRuntime {
  constructor({ requestImpl = https.request, cacheTTL = 300_000 } = {}) {
    this.requestImpl = requestImpl;
    this.cacheTTL = cacheTTL;
    this.catalogs = new Map();
    this.active = new Set();
    this.stopping = false;
  }

  async catalog(apiKey, signal) {
    signal?.throwIfAborted();
    const token = String(apiKey).trim();
    const key = modelFingerprint(token);
    let item = this.catalogs.get(key);
    if (!item || (!item.pending && item.expires <= Date.now())) {
      const controller = new AbortController();
      item = { expires: Date.now() + this.cacheTTL, controller, pending: true };
      item.promise = this.callUnary(CATALOG_PATH, token, { signal: controller.signal })
        .then((raw) => decodeCatalog(raw))
        .then((models) => {
          if (!models.length) throw new WindSurfError(502, "empty_model_catalog", "Windsurf returned no available models for this account.");
          return models;
        })
        .catch((error) => {
          if (this.catalogs.get(key) === item) this.catalogs.delete(key);
          throw error;
        })
        .finally(() => { item.pending = false; });
      this.catalogs.set(key, item);
      if (this.catalogs.size > 64) this.catalogs.delete(this.catalogs.keys().next().value);
    }
    return withAbort(item.promise, signal);
  }

  async models(apiKey, signal) {
    return this.catalog(apiKey, signal);
  }

  generate(apiKey, request, options = {}) {
    const operation = this.generateRequest(apiKey, request, options);
    this.active.add(operation);
    operation.then(() => this.active.delete(operation), () => this.active.delete(operation));
    return operation;
  }

  async generateRequest(apiKey, request, { signal, onText, onThinking } = {}) {
    signal?.throwIfAborted();
    const token = String(apiKey).trim();
    const catalog = await this.catalog(token, signal);
    signal?.throwIfAborted();
    if (this.stopping) throw new WindSurfError(503, "adapter_shutdown", "Windsurf adapter is shutting down.");
    const model = resolveModel(catalog, request.model);
    if (request.tools?.length) {
      throw new WindSurfError(400, "unsupported_tools", "Windsurf currently supports text conversations without caller tools.");
    }
    const proto = buildChatRequest({ token, messages: request.messages, model, sessionId: request.session_id, completion: request.completion });
    const framed = wrapEnvelope(proto, { compress: false });

    const controller = new AbortController();
    const onAbort = () => controller.abort(signal.reason);
    signal?.addEventListener("abort", onAbort, { once: true });
    try {
      return await withTimeout(this.streamChat(token, framed, { controller, onText, onThinking }), request.timeoutMs ?? 600_000, controller);
    } finally {
      signal?.removeEventListener("abort", onAbort);
    }
  }

  streamChat(token, framed, { controller, onText, onThinking, deadlineMs = 600_000 }) {
    return new Promise((resolve, reject) => {
      const parser = new FrameParser();
      const contentDecoder = new StringDecoder("utf8");
      const reasoningDecoder = new StringDecoder("utf8");
      let content = "";
      let reasoning = "";
      let usage = null;
      let settled = false;
      const done = (fn, value) => { if (settled) return; settled = true; clearTimeout(deadline); fn(value); };
      const deadline = setTimeout(() => {
        controller.abort(new WindSurfError(504, "windsurf_timeout", "Windsurf request timed out."));
      }, deadlineMs);
      deadline.unref?.();

      const req = this.requestImpl({
        hostname: HOST,
        port: 443,
        path: CHAT_PATH,
        method: "POST",
        signal: controller.signal,
        headers: connectHeaders({
          authorization: authHeader(token),
          "Content-Length": framed.length,
          Accept: "*/*",
        }),
      }, (res) => {
        if (res.statusCode !== 200) {
          const chunks = [];
          res.on("data", (c) => chunks.push(c));
          res.on("end", () => done(reject, classifyUpstreamError(res.statusCode, Buffer.concat(chunks).toString("utf8"))));
          res.on("error", (error) => done(reject, error));
          return;
        }
        res.on("data", (chunk) => {
          let frames;
          try { frames = parser.push(chunk); }
          catch (error) { controller.abort(error); done(reject, error); return; }
          for (const frame of frames) {
            if (frame.isEndStream) {
              const text = frame.payload.toString("utf8").trim();
              if (text && text !== "{}") {
                try {
                  const parsed = JSON.parse(text);
                  if (parsed?.error) {
                    done(reject, classifyUpstreamError(parsed.error.status || 502, JSON.stringify(parsed.error)));
                    return;
                  }
                } catch { /* non-JSON trailer is success */ }
              }
              done(resolve, { content, reasoning, usage });
              return;
            }
            const decoded = decodeFrame(frame.payload);
            if (decoded.contentBytes) {
              const text = contentDecoder.write(decoded.contentBytes);
              if (text) { content += text; void onText?.(text); }
            }
            if (decoded.reasoningBytes) {
              const text = reasoningDecoder.write(decoded.reasoningBytes);
              if (text) { reasoning += text; void onThinking?.(text); }
            }
            if (decoded.usage) usage = decoded.usage;
          }
        });
        res.on("error", (error) => done(reject, error));
        res.on("end", () => done(reject, new WindSurfError(502, "windsurf_stream_truncated", "Windsurf stream ended before a terminal frame.")));
      });
      req.on("error", (error) => {
        if (controller.signal.aborted) done(reject, controller.signal.reason || error);
        else done(reject, error);
      });
      req.end(framed);
    });
  }

  callUnary(path, token, { signal } = {}) {
    return new Promise((resolve, reject) => {
      const body = writeMessageField(1, buildMetadataLocal(token));
      const controller = new AbortController();
      const onAbort = () => controller.abort(signal?.reason);
      signal?.addEventListener("abort", onAbort, { once: true });
      const req = this.requestImpl({
        hostname: HOST,
        port: 443,
        path,
        method: "POST",
        signal: controller.signal,
        headers: {
          "Content-Type": "application/proto",
          "Connect-Protocol-Version": "1",
          "Content-Length": body.length,
          "User-Agent": "connect-es/2.0.0",
          authorization: authHeader(token),
          Accept: "*/*",
        },
      }, (res) => {
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          signal?.removeEventListener("abort", onAbort);
          const raw = Buffer.concat(chunks);
          if (res.statusCode !== 200) return reject(classifyUpstreamError(res.statusCode, raw.toString("utf8")));
          resolve(raw);
        });
        res.on("error", reject);
      });
      req.on("error", (error) => {
        signal?.removeEventListener("abort", onAbort);
        reject(controller.signal.aborted ? (controller.signal.reason || error) : error);
      });
      req.end(body);
    });
  }

  async stop() {
    this.stopping = true;
    for (const item of this.catalogs.values()) item.controller.abort(new WindSurfError(503, "adapter_shutdown", "Windsurf adapter is shutting down."));
    await Promise.allSettled([...this.active]);
  }
}

function withAbort(promise, signal) {
  if (!signal) return promise;
  if (signal.aborted) return Promise.reject(signal.reason);
  return Promise.race([
    promise,
    new Promise((_, reject) => signal.addEventListener("abort", () => reject(signal.reason), { once: true })),
  ]);
}

export function resolveModel(catalog, requested) {
  const name = String(requested ?? "").trim();
  if (!name) throw new WindSurfError(400, "invalid_model", "A model selector is required.");
  for (const model of catalog) {
    if (model.selector === name || model.alias === name) return model.selector;
  }
  throw new WindSurfError(400, "model_not_found", "The requested model is absent from this Windsurf account's catalog.");
}

// Local metadata helper for unary calls (catalog), mirroring buildClientMetadata.
function buildMetadataLocal(token) {
  return Buffer.concat([
    writeStringField(1, "chisel"),
    writeStringField(2, "2026.8.18"),
    writeStringField(3, token),
    writeStringField(4, "en"),
    writeStringField(5, "linux"),
    writeStringField(7, "2026.8.18"),
    writeStringField(12, "chisel"),
    writeStringField(31, randomBytes(366).toString("hex")),
  ]);
}
