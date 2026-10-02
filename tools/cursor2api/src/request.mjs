export class CursorError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

const acceptedFields = new Set([
  "model", "messages", "stream", "stream_options", "n", "tools", "tool_choice",
  "parallel_tool_calls", "reasoning_effort", "user", "metadata", "session_id",
  "conversation_id", "prompt_cache_key", "store",
]);
const unsupportedFields = new Set([
  "functions", "function_call", "temperature", "top_p", "max_tokens",
  "max_completion_tokens", "stop", "seed", "logprobs", "top_logprobs",
  "logit_bias", "response_format", "modalities", "audio", "prediction",
  "presence_penalty", "frequency_penalty", "service_tier", "web_search_options",
]);

export function normalizeRequest(body) {
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    throw new CursorError(400, "invalid_request", "Request must be a JSON object.");
  }
  if (typeof body.model !== "string" || !body.model.trim() || body.model.length > 512) {
    throw new CursorError(400, "invalid_model", "A model ID from /v1/models is required.");
  }
  if (!Array.isArray(body.messages) || !body.messages.length) {
    throw new CursorError(400, "invalid_messages", "messages must be a non-empty array.");
  }
  if (body.stream !== undefined && typeof body.stream !== "boolean") {
    throw new CursorError(400, "invalid_stream", "stream must be a boolean.");
  }
  if (body.tools != null && (!Array.isArray(body.tools) || body.tools.length)) {
    throw new CursorError(400, "unsupported_tools", "Cursor currently supports text conversations without caller tools.");
  }
  if (body.tool_choice != null && body.tool_choice !== "none") {
    throw new CursorError(400, "unsupported_tools", "Only tool_choice=none is supported.");
  }
  if (body.parallel_tool_calls != null && typeof body.parallel_tool_calls !== "boolean") {
    throw new CursorError(400, "invalid_request", "parallel_tool_calls must be a boolean.");
  }
  if (body.n != null && body.n !== 1) {
    throw new CursorError(400, "unsupported_parameter", "Only n=1 is supported.");
  }
  if (body.reasoning_effort != null && (typeof body.reasoning_effort !== "string" || !body.reasoning_effort)) {
    throw new CursorError(400, "invalid_reasoning_effort", "reasoning_effort must be a non-empty string.");
  }
  for (const field of Object.keys(body)) {
    if ((!acceptedFields.has(field) && !unsupportedFields.has(field)) || (unsupportedFields.has(field) && body[field] != null)) {
      throw new CursorError(400, "unsupported_parameter", `${field} has no supported Cursor SDK mapping.`);
    }
  }
  if (body.stream_options != null) {
    const options = body.stream_options;
    if (typeof options !== "object" || Array.isArray(options) || Object.keys(options).some((key) => key !== "include_usage") || (options.include_usage !== undefined && typeof options.include_usage !== "boolean")) {
      throw new CursorError(400, "invalid_stream_options", "Only stream_options.include_usage (boolean) is supported.");
    }
  }
  const messages = body.messages.map((message) => {
    if (!message || !["system", "developer", "user", "assistant"].includes(message.role)) {
      throw new CursorError(400, "unsupported_message", "Only system, developer, user and assistant text messages are supported.");
    }
    if (Object.keys(message).some((field) => !["role", "content", "name"].includes(field) && message[field] != null)) {
      throw new CursorError(400, "unsupported_message", "Tool calls and non-text message fields are not supported.");
    }
    let content = message.content;
    if (Array.isArray(content)) {
      if (content.some((part) => !part || part.type !== "text" || typeof part.text !== "string" || Object.keys(part).some((field) => field !== "type" && field !== "text"))) {
        throw new CursorError(400, "unsupported_content", "Images, files and audio are not supported by this Cursor adapter.");
      }
      content = content.map((part) => part.text).join("\n");
    }
    if (typeof content !== "string" || (message.name != null && typeof message.name !== "string")) {
      throw new CursorError(400, "invalid_content", "Message content must be text.");
    }
    return { role: message.role, content, ...(message.name ? { name: message.name } : {}) };
  });
  if (messages.at(-1).role !== "user" || !messages.at(-1).content.trim()) {
    throw new CursorError(400, "invalid_turn", "The current turn must end with a non-empty user message.");
  }
  // SDK 接收单个提示；保留所有角色和历史，不伪造原生 Chat 消息。
  const prompt = [
    "The following JSON is the caller's complete conversation.",
    "Apply system and developer instructions, use assistant messages as history, and answer the last user message.",
    "Return an answer as text. No filesystem, shell, network or interactive tools are available.",
    JSON.stringify(messages),
  ].join("\n\n");
  return {
    model: body.model.trim(),
    prompt,
    stream: body.stream === true,
    includeUsage: body.stream_options?.include_usage === true,
    reasoningEffort: body.reasoning_effort ?? undefined,
  };
}

export function usageFromSDK(usage) {
  if (!usage) return undefined;
  const fields = ["inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens", "totalTokens"];
  if (fields.some((key) => !Number.isSafeInteger(usage[key]) || usage[key] < 0) || (usage.reasoningTokens !== undefined && (!Number.isSafeInteger(usage.reasoningTokens) || usage.reasoningTokens < 0 || usage.reasoningTokens > usage.outputTokens))) {
    throw new CursorError(502, "invalid_usage", "Cursor returned invalid token usage.");
  }
  const promptTokens = usage.inputTokens + usage.cacheReadTokens + usage.cacheWriteTokens;
  if (!Number.isSafeInteger(promptTokens) || promptTokens + usage.outputTokens !== usage.totalTokens) {
    throw new CursorError(502, "invalid_usage", "Cursor returned inconsistent token usage.");
  }
  return {
    prompt_tokens: promptTokens,
    completion_tokens: usage.outputTokens,
    total_tokens: usage.totalTokens,
    prompt_tokens_details: { cached_tokens: usage.cacheReadTokens, cache_creation_tokens: usage.cacheWriteTokens },
    ...(usage.reasoningTokens !== undefined ? { completion_tokens_details: { reasoning_tokens: usage.reasoningTokens } } : {}),
  };
}

export function publicError(error) {
  if (error instanceof CursorError) return error;
  const name = error?.name ?? "";
  const code = String(error?.code ?? "").toLowerCase();
  const status = Number(error?.status);
  if (name === "AuthenticationError" || status === 401 || ["unauthenticated", "invalid_api_key", "bad_api_key", "bad_user_api_key", "not_logged_in", "auth_token_not_found", "auth_token_expired", "invalid_auth_id", "agent_requires_login", "unauthorized"].includes(code)) {
    return new CursorError(401, "cursor_authentication_failed", "Cursor rejected the account API key.");
  }
  if (name === "RateLimitError" || status === 429 || ["resource_exhausted", "rate_limit_exceeded", "usage_limit_exceeded", "free_user_usage_limit", "pro_user_usage_limit", "free_user_rate_limit_exceeded", "pro_user_rate_limit_exceeded", "openai_rate_limit_exceeded", "generic_rate_limit_exceeded", "gpt_4_vision_preview_rate_limit", "rate_limited", "rate_limited_changeable", "api_key_rate_limit"].includes(code)) {
    return new CursorError(429, "rate_limit_exceeded", "Cursor account rate or usage limit reached.");
  }
  // Cloud Agent（SDK 的全部文本能力）只对 Cursor Pro 开放；免费账号必须在授权后就能看出这一点。
  if (["plan_required", "upgrade_required", "subscription_required"].includes(code)) {
    return new CursorError(403, "cursor_plan_required", "The linked Cursor account is on the free plan, but Cursor Cloud Agent requires Pro.");
  }
  if (status === 403 || ["permission_denied", "model_access_denied", "model_blocked", "not_high_enough_permissions"].includes(code)) {
    return new CursorError(403, "cursor_permission_denied", "Cursor account is not permitted to use this model.");
  }
  if (name === "ConfigurationError" || status === 400 || ["invalid_argument", "bad_model_name", "bad_request"].includes(code)) {
    return new CursorError(400, "cursor_configuration_error", "Cursor rejected the model or account configuration.");
  }
  if (status === 504 || ["deadline_exceeded", "timeout"].includes(code)) {
    return new CursorError(504, "cursor_timeout", "Cursor generation timed out.");
  }
  return new CursorError(502, "cursor_upstream_error", "Cursor did not complete the request successfully.");
}
