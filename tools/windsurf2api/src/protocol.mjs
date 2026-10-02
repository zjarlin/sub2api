import { randomBytes, createHash, randomUUID } from "node:crypto";
import {
  writeStringField, writeVarintField, writeMessageField, writeFixed64Field,
  parseFields, getField, f64le,
} from "./proto.mjs";

export class WindSurfError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

// Client identity the upstream expects (matches the official CLI wire).
const CLIENT_NAME = "chisel";
const CLIENT_VERSION = "2026.8.18";
const SOURCE = Object.freeze({ USER: 1, ASSISTANT: 2, TOOL_RESULT: 4 });

const DEFAULT_CONTEXT_WINDOW = 128000;
const DEFAULT_MAX_TOKENS = 8192;
const DEFAULT_TEMPERATURE = 1.0;
const MIN_TEMPERATURE = 0.001;
const DEFAULT_TOP_K = 40;
const DEFAULT_TOP_P = 0.95;

export function sessionToken(apiKey) {
  const value = String(apiKey ?? "").trim();
  if (!value) throw new WindSurfError(401, "missing_windsurf_key", "A Windsurf session token is required.");
  return value;
}

function fingerprint() {
  return randomBytes(366).toString("hex");
}

export function buildClientMetadata(token) {
  return Buffer.concat([
    writeStringField(1, CLIENT_NAME),
    writeStringField(2, CLIENT_VERSION),
    writeStringField(3, token),
    writeStringField(4, "en"),
    writeStringField(5, "linux"),
    writeStringField(7, CLIENT_VERSION),
    writeStringField(12, CLIENT_NAME),
    writeStringField(31, fingerprint()),
  ]);
}

function buildCompletionConfig({ maxTokens, temperature, topK, topP, contextWindow } = {}) {
  let temp = temperature ?? DEFAULT_TEMPERATURE;
  if (temp < MIN_TEMPERATURE) temp = MIN_TEMPERATURE;
  return Buffer.concat([
    writeVarintField(1, 1),
    writeVarintField(2, maxTokens ?? DEFAULT_MAX_TOKENS),
    writeVarintField(3, contextWindow ?? DEFAULT_CONTEXT_WINDOW),
    writeFixed64Field(5, f64le(temp)),
    writeVarintField(7, topK ?? DEFAULT_TOP_K),
    writeFixed64Field(8, f64le(topP ?? DEFAULT_TOP_P)),
  ]);
}

function messageText(content) {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) return content.filter((c) => c?.type === "text").map((c) => c.text).join("\n");
  return "";
}

function mergeTextMessages(messages) {
  const merged = [];
  for (const m of messages || []) {
    const pi = merged.length - 1;
    const prev = pi >= 0 ? merged[pi] : null;
    const mergeable = (x) => x && (x.role === "user" || x.role === "assistant");
    if (prev && mergeable(prev) && mergeable(m) && prev.role === m.role) {
      const a = messageText(prev.content);
      const b = messageText(m.content);
      merged[pi] = { ...prev, content: b ? (a ? `${a}\n\n${b}` : b) : a };
      continue;
    }
    merged.push(m);
  }
  return merged;
}

// Build a GetChatMessageRequest protobuf. Only text turns are supported; tools
// and non-text modalities are rejected before we get here.
export function buildChatRequest({ token, messages, model, sessionId, completion } = {}) {
  if (!sessionToken(token)) throw new WindSurfError(401, "missing_windsurf_key", "A Windsurf session token is required.");
  if (!model) throw new WindSurfError(400, "invalid_model", "A model selector is required.");

  let systemPrompt = "";
  const chatMessages = [];
  for (const msg of mergeTextMessages(messages)) {
    if (msg.role === "system") {
      const t = messageText(msg.content);
      systemPrompt += systemPrompt ? `\n${t}` : t;
      continue;
    }
    if (msg.role === "assistant" && messageText(msg.content).trim() === "") continue;
    const source = msg.role === "assistant" ? SOURCE.ASSISTANT : SOURCE.USER;
    chatMessages.push(Buffer.concat([
      writeStringField(1, randomUUID()),
      writeVarintField(2, source),
      writeStringField(3, messageText(msg.content)),
    ]));
  }

  const modelConfig = Buffer.concat([
    writeStringField(1, randomUUID()),
    writeVarintField(2, 1),
    writeVarintField(3, 4),
  ]);

  const parts = [
    writeMessageField(1, buildClientMetadata(token)),
    writeStringField(2, systemPrompt),
  ];
  for (const cm of chatMessages) parts.push(writeMessageField(3, cm));
  parts.push(
    writeVarintField(7, 5),
    writeMessageField(8, buildCompletionConfig(completion)),
    writeMessageField(15, modelConfig),
    writeStringField(16, sessionId || randomUUID()),
    writeVarintField(20, 1),
    writeStringField(21, model),
  );
  return Buffer.concat(parts);
}

// GetChatMessageResponse layout, calibrated against live captures:
//   #3 content text, #5 finish enum (2 == stop), #7 metadata {#2 prompt, #3
//   completion, #4 cache_write, #5 cache_read, #9 model}, #9 reasoning text.
const FIELD = Object.freeze({ CONTENT: 3, FINISH: 5, META: 7, REASONING: 9 });

function readNumeric(fields, number) {
  const f = getField(fields, number);
  if (!f) return null;
  if (f.wireType === 0) return Number(f.value);
  if (f.wireType === 1) return f.value.readDoubleLE(0);
  if (f.wireType === 5) return f.value.readFloatLE(0);
  return null;
}

export function decodeFrame(payload) {
  let fields;
  try {
    fields = parseFields(payload);
  } catch {
    return { contentBytes: null, reasoningBytes: null, finish: null, usage: null };
  }
  const content = getField(fields, FIELD.CONTENT, 2);
  const reasoning = getField(fields, FIELD.REASONING, 2);
  const finish = getField(fields, FIELD.FINISH, 0);
  const meta = getField(fields, FIELD.META, 2);
  let usage = null;
  if (meta) {
    let mf;
    try { mf = parseFields(meta.value); } catch { mf = []; }
    const prompt = readNumeric(mf, 2);
    const completion = readNumeric(mf, 3);
    const cacheWrite = readNumeric(mf, 4);
    const cacheRead = readNumeric(mf, 5);
    if (completion != null) {
      usage = normalizeUsage({ prompt, completion, cacheRead, cacheWrite });
    }
  }
  return {
    contentBytes: content ? content.value : null,
    reasoningBytes: reasoning ? reasoning.value : null,
    finish: finish ? finish.value : null,
    usage,
  };
}

export function normalizeUsage({ prompt, completion, cacheRead, cacheWrite } = {}) {
  if (completion == null) return null;
  const fresh = prompt || 0;
  const read = cacheRead || 0;
  const write = cacheWrite || 0;
  const promptTokens = fresh + read;
  return {
    prompt_tokens: promptTokens,
    completion_tokens: completion,
    total_tokens: promptTokens + completion + write,
    ...(read ? { prompt_tokens_details: { cached_tokens: read } } : {}),
    ...(write ? { cache_creation_input_tokens: write } : {}),
  };
}

// GetCliModelConfigs: response field #1 repeated ClientModelConfig, with
// #1 label, #4 disabled, #10 provider, #22 selector, #23.#23 alias.
export function decodeCatalog(raw) {
  const configs = parseFields(raw).filter((f) => f.field === 1 && f.wireType === 2);
  const out = [];
  for (const c of configs) {
    const fields = parseFields(c.value);
    const selectorField = getField(fields, 22, 2);
    if (!selectorField) continue;
    const disabled = getField(fields, 4, 0);
    if (disabled && Number(disabled.value) === 1) continue;
    const selector = selectorField.value.toString("utf8");
    const labelField = getField(fields, 1, 2);
    const providerField = getField(fields, 10, 0);
    let alias = "";
    const info = getField(fields, 23, 2);
    if (info) {
      try {
        const af = getField(parseFields(info.value), 23, 2);
        if (af) alias = af.value.toString("utf8");
      } catch { /* keep '' */ }
    }
    out.push({
      selector,
      label: labelField ? labelField.value.toString("utf8") : selector,
      alias,
      provider: providerField ? Number(providerField.value) : null,
    });
  }
  return out;
}

export function modelFingerprint(apiKey) {
  return createHash("sha256").update(String(apiKey)).digest("hex");
}

export function classifyUpstreamError(status, body) {
  const text = String(body || "").slice(0, 500).toLowerCase();
  if (status === 401 || status === 403 || text.includes("permission_denied") || text.includes("unauthenticated")) {
    return new WindSurfError(401, "windsurf_authentication_failed", "Windsurf rejected the saved session token; sign in again.");
  }
  if (status === 429 || text.includes("resource_exhausted") || text.includes("rate limit")) {
    return new WindSurfError(429, "windsurf_rate_limited", "Windsurf account rate or usage limit reached.");
  }
  if (text.includes("/upgrade") || text.includes("upgrade to access")) {
    return new WindSurfError(402, "windsurf_plan_required", "This Windsurf model is not included in the account plan.");
  }
  if (status >= 500 || text.includes("unavailable") || text.includes("internal error")) {
    return new WindSurfError(502, "windsurf_upstream_error", "Windsurf upstream returned an error.");
  }
  return new WindSurfError(status || 502, "windsurf_upstream_error", "Windsurf upstream request failed.");
}

export function publicError(error) {
  if (error instanceof WindSurfError) return error;
  const status = Number.isInteger(error?.status) ? error.status : 502;
  return new WindSurfError(status, error?.code || "windsurf_adapter_error", error?.message || "Windsurf adapter request failed.");
}
