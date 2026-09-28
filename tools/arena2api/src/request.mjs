import crypto from "node:crypto";

const unsupportedParameters = new Set([
  "functions", "function_call", "tool_choice", "response_format", "temperature", "top_p",
  "max_tokens", "max_completion_tokens", "stop", "seed", "logprobs", "top_logprobs",
  "logit_bias", "reasoning_effort", "modalities", "audio", "prediction", "presence_penalty",
  "frequency_penalty", "parallel_tool_calls", "service_tier", "store", "web_search_options",
]);
const acceptedParameters = new Set([
  "model", "messages", "stream", "stream_options", "tools", "n", "user", "metadata",
  "session_id", "conversation_id", "prompt_cache_key",
]);

export class ArenaError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export function identity(value) {
  if (typeof value !== "string" || !value.trim() || value.length > 512 || /[\x00-\x1f\x7f]/.test(value)) {
    throw new ArenaError(400, "session_identity_required", "Send a stable X-Arena-Session-Id (maximum 512 characters).");
  }
  return value.trim();
}

export function normalizeRequest(body) {
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    throw new ArenaError(400, "invalid_request", "Request must be a JSON object.");
  }
  if (typeof body.model !== "string" || !body.model.trim()) {
    throw new ArenaError(400, "invalid_model", "model is required.");
  }
  if (!Array.isArray(body.messages) || !body.messages.length) {
    throw new ArenaError(400, "invalid_messages", "messages must be a non-empty array.");
  }
  if (body.stream !== undefined && typeof body.stream !== "boolean") {
    throw new ArenaError(400, "invalid_stream", "stream must be a boolean.");
  }
  if (body.tools !== undefined && body.tools !== null && (!Array.isArray(body.tools) || body.tools.length)) {
    throw new ArenaError(400, "unsupported_tools", "This Arena adapter currently supports text conversations only.");
  }
  for (const field of Object.keys(body)) {
    if ((!acceptedParameters.has(field) && !unsupportedParameters.has(field)) || (unsupportedParameters.has(field) && body[field] !== null)) {
      throw new ArenaError(400, "unsupported_parameter", `${field} is not supported by the Arena browser protocol.`);
    }
  }
  if (body.stream_options !== undefined && body.stream_options !== null) {
    const options = body.stream_options;
    if (typeof options !== "object" || Array.isArray(options) || Object.keys(options).some((field) => field !== "include_usage") || (options.include_usage !== undefined && typeof options.include_usage !== "boolean")) {
      throw new ArenaError(400, "unsupported_parameter", "Only stream_options.include_usage (boolean) is accepted; usage remains unavailable.");
    }
  }
  if (body.n !== undefined && body.n !== null && body.n !== 1) {
    throw new ArenaError(400, "unsupported_parameter", "Only n=1 is supported.");
  }
  const messages = body.messages.map((message) => {
    if (!message || !["system", "developer", "user", "assistant"].includes(message.role) || message.tool_calls || message.function_call) {
      throw new ArenaError(400, "unsupported_message", "Only system, developer, user and assistant text messages are supported.");
    }
    if (Object.keys(message).some((field) => field !== "role" && field !== "content" && message[field] !== null)) {
      throw new ArenaError(400, "unsupported_message", "Message fields other than role and text content are not supported.");
    }
    let content = message.content;
    if (Array.isArray(content)) {
      if (content.some((part) => !part || part.type !== "text" || typeof part.text !== "string" || Object.keys(part).some((field) => field !== "type" && field !== "text"))) {
        throw new ArenaError(400, "unsupported_content", "Images, files and audio are not supported.");
      }
      content = content.map((part) => part.text).join("\n");
    }
    if (typeof content !== "string") {
      throw new ArenaError(400, "invalid_content", "Message content must be text.");
    }
    return { role: message.role, content };
  });
  if (messages.at(-1).role !== "user" || !messages.at(-1).content.trim()) {
    throw new ArenaError(400, "invalid_turn", "The current turn must end with a non-empty user message.");
  }
  const prompt = [
    "The following JSON is the caller's complete conversation for this turn.",
    "Apply system and developer instructions, use assistant messages as history, and answer only the last user message.",
    "Earlier Arena messages are transport history; this supplied conversation is authoritative.",
    "Return text only. Do not execute Arena sandbox tools or request interactive input.",
    JSON.stringify(messages),
  ].join("\n\n");
  if (prompt.length > 64_000) {
    throw new ArenaError(413, "context_too_large", "Conversation exceeds the adapter's 64000-character limit.");
  }
  return { model: body.model.trim(), messages, prompt, stream: body.stream === true };
}

export function requestDigest(request) {
  return crypto.createHash("sha256").update(JSON.stringify({ model: request.model, messages: request.messages })).digest("hex");
}

export function completion(model, content) {
  return {
    id: `chatcmpl-arena-${crypto.randomUUID()}`,
    object: "chat.completion",
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, message: { role: "assistant", content }, finish_reason: "stop" }],
  };
}

export function validateReply(payload) {
  const content = payload?.choices?.[0]?.message?.content;
  if (typeof content !== "string" || !content.trim() || /^\((?:empty Agent response|Arena stream error:|Arena agent (?:attempted|is waiting))/.test(content.trim())) {
    throw new ArenaError(502, "arena_generation_failed", "Arena did not return a completed text answer.");
  }
  return content;
}
