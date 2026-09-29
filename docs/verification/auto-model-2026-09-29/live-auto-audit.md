# Live auto audit — 2026-09-29

Checked gateway http://192.168.31.252:18080; public catalog returned 331 IDs. All probes used synthetic prompts and unique UUID sessions. No credentials, account identifiers or user prompts are stored.

| Check | HTTP | Result | Time | Attempts |
| --- | --- | --- | --- | --- |
| Plain text | 200 | deepseek-v4.1-flash; completed; exact OK | 2.565 s | deepseek-v4.1-flash |
| Inert function call | 200 | glm-5.3; completed; health_ping(value=OK) | 9.049 s | deepseek-v4.1-flash → deepseek-v4.1-flash → glm-5.3 |

The repeated DeepSeek name represents separate candidate platforms (openai, opencode_go). No function was executed. Initial observation reads can precede asynchronous persistence; bounded later reads showed completed for both sessions.

Both requests produced the same plan: 894 model/platform entries, 754 unique model IDs, 161 eligible entries covering 134 unique model IDs. Exclusions: 588 no_compatible_account, 81 protocol_not_supported, 46 not_text_generation, 18 auto_policy_excluded. Eligibility reflects configured account compatibility, not an inference-success guarantee.

32 of the 331 listed IDs had no exact model or alias entry in the plan:

- media_names_rejected_by_local_text_guard_if_present (6): gpt-image-2.5, grok-imagine-video-1.5, opc-image-v1, opc-image-v2, opc-video-v1, opc-video-v2

- similar_existing_candidate_name_or_platform_alias (9): claude-fable-5.1, claude-haiku-4-5, deepseek-v4-1-flash, glm-5-2, ocg/deepseek-v4-flash, ocg/deepseek-v4-pro, qwen-3.8-max, qwen/qwen3.8-max, sensenova-6-8-flash-lite

- dated_variants_not_present_as_exact_ids (4): deepseek-v4-flash-0731, deepseek-v4-pro-0813, deepseek-v4.1-flash-0910, qwen3.8-max-0902

- other_catalog_only_ids_with_no_exact_plan_entry (13): claude-opus-4-6, gemini-3.1-flash-lite, gemini-3.1-pro, gemini-3.5-flash, gemini-3.6-flash, gemini-3.7-flash, gemini-3.8-flash, grok-build-0.1, hy4, mercury-2.5, opc-txt-v1, opc-txt-v2, sensenova-u1-fast

The auto virtual entry is present (platform qoder) and excluded as not_text_generation; it is not one of the 32 omissions. The six image/video IDs should not be text-auto candidates, but their absent metadata is still distinct from an explicit exclusion reason. Similar names and date variants are not treated as equivalent without upstream evidence.

Local source shows discovery can use account catalogs, static defaults, pinned manifests and caches; auto inventory reads account mapping keys and upstream snapshot IDs. The exact source of these 32 live omissions cannot be proved from the public APIs used here. Unifying these sources and explaining discovery-only entries is preferable to making unknown IDs eligible.

Full sanitized evidence and candidate plans: [live-auto-audit.json](./live-auto-audit.json). These two successful requests establish current text/tool routing and an observed multi-step fallback, not that all 331 models work or that auto can succeed during total upstream failure.
