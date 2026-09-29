# NVIDIA Kimi K3 reasoning compatibility

Observed incident: the NVIDIA OpenAI-compatible upstream rejected `moonshotai/kimi-k3` with `Unsupported Kimi K3 thinking_effort="medium"; supported values are low, high, max`.

Official NVIDIA documentation checked on 2026-09-29:

- https://docs.api.nvidia.com/nim/reference/moonshotai-kimi-k3: low/high/max reasoning effort; thinking is always enabled.
- https://build.nvidia.com/moonshotai/kimi-k3/modelcard: the API schema uses `reasoning_effort`, accepts low/high/max, and defaults to max when omitted. `thinking_effort` in the error is the upstream's internal terminology; the gateway continues sending the documented `reasoning_effort` field.

The adapter applies only to OpenAI-platform accounts whose base URL hostname is exactly `integrate.api.nvidia.com`, after mapping to exact `kimi-k3` or `moonshotai/kimi-k3`. It maps minimal to low, medium to high, xhigh/extrahigh to max, and preserves valid low/high/max. It leaves none, omitted, non-string and unknown values unchanged. It does not expand token ceilings or infer that proxy aliases share NVIDIA's contract.

Medium maps upward because NVIDIA has no medium tier. This avoids silently reducing requested reasoning strength, but can use a higher native effort; the maximum token budget is preserved. A read-only transaction confirmed production group 6 currently has an empty max_reasoning_effort and over-limit mode downgrade; the incident therefore had no configured group ceiling. Configured ceilings are nevertheless enforced after native normalization: the adapter reads the bound policy or current API key group. If the native value would exceed the ceiling, Auto advances through capability fallback without counting an account failure; an explicitly selected model receives local HTTP 403. Neither path sends an over-limit upstream request. The existing generic third-party Responses handling strips a catalog-only none placeholder before this adapter; NVIDIA K3's always-on reasoning means none must not be described as a supported thinking-off mode.

Responses-to-Chat, native Chat Completions and Anthropic-to-Chat forwarding all apply the adapter and record the resulting effective reasoning effort in usage metadata. Tests use an in-memory HTTP recorder and exercise all three paths, account model aliases, unchanged token limits, valid/unknown/none values, scope exclusions, three protocol paths with bound/group ceilings, Auto fallback without committing output, and no upstream call when a ceiling blocks the native value.

Passed from backend:

`go test -tags unit ./internal/service -run 'TestNormalizeNVIDIAKimi|TestNVIDIAKimi|TestAutoModelKimiEffortMismatch|TestForwardResponses_ForceChatCompletions|TestForwardAsAnthropic_ForceChatCompletions|TestForwardAsRawChatCompletions|TestNormalizeGLMOpenAIReasoningEffort|TestNormalizeAgnes' -count=1`

The focused implementation phase made no supplier inference calls or commits. This change was subsequently deployed with `sub2api:auto-recovery-20260929` at 22:14 Asia/Shanghai; see [release verification](auto-recovery.md). No additional supplier inference request was used to validate the rollout.
