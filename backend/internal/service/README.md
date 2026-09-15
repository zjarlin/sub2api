# Application Services

`account_recovery.go` selects temporary recovery candidates only after the normal
pool is exhausted. A closed scheduling switch always excludes an account, and
automatic recovery never enables that switch.

`reasoning_replay.go` preserves exact DeepSeek Responses reasoning content in
API-key-scoped cache references. Only an explicit missing-reasoning rejection
allows a bounded, observable non-thinking retry for unrecoverable old history.

`upstream_concurrency.go` recognizes explicit in-flight capacity rejections.
They cause a short runtime cooldown, not a model-support or account-health failure.

Agnes Responses forwarding lowers namespace-only tool declarations through the
shared client-tool adapter, including history and tool choice. JSON and SSE
responses restore the original namespace/name; native namespace routes keep
their existing behavior.
Text-only assistant output messages are replayed as input messages (`role` and
string `content`) for Agnes. This avoids its output-schema validation during
tool-result replay; reasoning, tool calls, and multimodal parts are unchanged.
