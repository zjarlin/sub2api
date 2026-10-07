# Vertical Intent Routing

The HTTP Responses and Chat Completions gateway runs vertical routing after API
key authentication, model aliases and the group model allowlist, before Auto
model selection. It applies to `ask`, `auto`, tier pools and explicit model IDs.

Only a fresh, text-only user command is eligible. Tool continuations,
`previous_response_id`, structured output, attachments, compound operations and
questions about implementing a feature stay on the normal model path.

- Translation: an explicit command must contain extractable source text and a
  supported target language. It uses the same configured translation aggregator
  as `/api/v1/translate`, without a text-model call or a classifier dependency.
- Images and videos: the command guard and Laya's calibrated choice probability
  must agree. Laya timeout, low confidence or a missing media route preserves the
  original request. Generation uses the existing media handlers, including
  authentication, moderation, concurrency, account selection and billing.

Configure `vertical_routing` in the existing Auto policy settings. The default
is enabled, minimum chosen-class probability `0.8`, and classifier timeout
`1500` ms. Media model IDs may be configured explicitly; otherwise models are
chosen from the current group's inventory. The same policy is editable under
System Settings / Gateway Services / Auto.

Responses and Chat keep their normal reply protocols. Cards use
`GET /v1/auto/routes`, scoped to the API key, session and turn. `operation` holds
the capability, actual provider, task ID, language codes and artifact URLs. It
does not contain source text, prompts, credentials or base64 media.

Video submission is `queued`, not `completed`. Reading route observations checks
up to two pending videos with bounded timeouts through the existing owner-bound
video status handler. Only an actual returned video artifact completes a task.
Codex Buddy displays translation, image and video cards using the same native
turn anchors as Auto routed; standard clients still receive ordinary replies.

This middleware does not intercept WebSocket response frames, Anthropic Messages
or Gemini-native requests. A missing route does not provision a media provider.
