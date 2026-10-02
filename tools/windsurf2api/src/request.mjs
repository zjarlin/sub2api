import { WindSurfError } from "./protocol.mjs";

const acceptedFields = new Set([
  "model", "messages", "stream", "stream_options", "n", "user", "metadata",
  "session_id", "conversation_id", "prompt_cache_key", "store", "temperature",
  "top_p", "max_tokens", "max_completion_tokens", "tools", "tool_choice",
  "parallel_tool_calls",
]);
const unsupportedFields = new Set([
  "functions", "function_call", "stop", "seed", "logprobs", "top_logprobs",
  "logit_bias", "response_format", "modalities", "audio", "prediction",
  "presence_penalty", "frequency_penalty", "service_tier", "web_search_options",
]);

export function normalizeRequest(body) {
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    throw new WindSurfError(400, "invalid_request", "Request must be a JSON object.");
  }
  if (typeof body.model !== "string" || !body.model.trim() || body.model.length > 512) {
    throw new WindSurfError(400, "invalid_model", "A model ID from /v1/models is required.");
  }
  if (!Array.isArray(body.messages) || !body.messages.length) {
    throw new WindSurfError(400, "invalid_messages", "messages must be a non-empty array.");
  }
  if (body.stream !== undefined && typeof body.stream !== "boolean") {
    throw new WindSurfError(400, "invalid_stream", "stream must be a boolean.");
  }
  if (body.tools != null && (!Array.isArray(body.tools) || body.tools.length)) {
    throw new WindSurfError(400, "unsupported_tools", "Windsurf currently supports text conversations without caller tools.");
  }
  if (body.tool_choice != null && body.tool_choice !== "none") {
    throw new WindSurfError(400, "unsupported_tools", "Only tool_choice=none is supported.");
  }
  if (body.n != null && body.n !== 1) {
    throw new WindSurfError(400, "unsupported_parameter", "Only n=1 is supported.");
  }
  for (const field of Object.keys(body)) {
    if ((!acceptedFields.has(field) && !unsupportedFields.has(field)) || (unsupportedFields.has(field) && body[field] != null)) {
      throw new WindSurfError(400, "unsupported_parameter", `${field} has no supported Windsurf mapping.`);
    }
  }
  if (body.stream_options != null) {
    const options = body.stream_options;
    if (typeof options !== "object" || Array.isArray(options) || Object.keys(options).some((key) => key !== "include_usage") || (options.include_usage !== undefined && typeof options.include_usage !== "boolean")) {
      throw new WindSurfError(400, "invalid_stream_options", "Only stream_options.include_usage (boolean) is supported.");
    }
  }
  const messages = body.messages.map((message) => {
    if (!message || !["system", "developer", "user", "assistant"].includes(message.role)) {
      throw new WindSurfError(400, "unsupported_message", "Only system, developer, user and assistant text messages are supported.");
    }
    let content = message.content;
    if (Array.isArray(content)) {
      if (content.some((part) => !part || part.type !== "text" || typeof part.text !== "string")) {
        throw new WindSurfError(400, "unsupported_content", "Images, files and audio are not supported by this Windsurf adapter.");
      }
      content = content.map((part) => part.text).join("\n");
    }
    if (typeof content !== "string") {
      throw new WindSurfError(400, "invalid_content", "Message content must be text.");
    }
    const role = message.role === "developer" ? "system" : message.role;
    return { role, content };
  });
  if (messages.at(-1).role !== "user" || !messages.at(-1).content.trim()) {
    throw new WindSurfError(400, "invalid_turn", "The current turn must end with a non-empty user message.");
  }
  const completion = {};
  if (typeof body.max_tokens === "number") completion.maxTokens = body.max_tokens;
  else if (typeof body.max_completion_tokens === "number") completion.maxTokens = body.max_completion_tokens;
  if (typeof body.temperature === "number") completion.temperature = body.temperature;
  if (typeof body.top_p === "number") completion.topP = body.top_p;
  return {
    model: body.model.trim(),
    messages,
    stream: body.stream === true,
    includeUsage: body.stream_options?.include_usage === true,
    session_id: typeof body.session_id === "string" ? body.session_id : undefined,
    completion,
  };
}

export function publicError(error) {
  if (error instanceof WindSurfError) return error;
  const status = Number.isInteger(error?.status) ? error.status : 502;
  return new WindSurfError(status, error?.code || "windsurf_adapter_error", error?.message || "Windsurf adapter request failed.");
}
