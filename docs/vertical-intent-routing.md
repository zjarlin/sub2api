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

## Multimodal Gateway Adapter

Explicit Responses `tool_choice: {"type":"image_generation"}` or `required`
with exactly one image-generation tool bypasses the intent classifier. Merely
declaring an available tool does not force generation. The tool's optional model
is checked against the group's inventory and allowlist. Image editing, reference
images, audio attachments and continuation inputs remain on the normal provider
path; the adapter never discards these inputs to manufacture a text-only prompt.
Neither user input nor system instructions receive injected tool instructions.

Responses image results contain `image_generation_call` with a stable item ID,
completed status, Base64 `result`, and detected `output_format`. URL results are
downloaded using the existing public-host-only image downloader, with redirect
checks, no upstream authorization header, a 60-second per-image timeout, and a
20 MiB per-image limit. PNG/JPEG/WebP are accepted; invalid or unavailable images
produce an error instead of a completed image item. Total Base64 payload is
limited to 64 MiB. Chat Completions keeps Markdown image links.

Wan/DashScope image models use the actual Chat Completions handler with a content
list. The upstream Chat service preserves their `output.choices` envelope and
records image count and output size for existing per-image billing. Unsupported
image tool options return an explicit parameter error. Standard
Images adapters forward size, quality, background, output format/compression and
moderation options. SSE emits image item added, in-progress, generating,
completed and item-done events before response completion. Generation remains
buffered: these are output lifecycle events, not provider partial-image previews.

`vertical_routing.image_fallback_models` configures up to four ordered exact model
IDs and is editable in the Auto settings panel. Only HTTP 429 or 5xx generation
failures trigger fallback. Candidates must be present in the group inventory and
allowed for the group; duplicate attempts are skipped. Failed image retrieval
after successful generation never submits a second generation request. Video
submissions are never retried across models because they can create duplicate
asynchronous jobs. Video refresh retains the task's recorded provider and the
existing owner-bound status lookup.

This adapter implements the Responses wire format; installed-client native image
rendering still requires acceptance on that client. It does not add an invented
video-generation Responses item, an audio API implementation, or MCP Apps support.
