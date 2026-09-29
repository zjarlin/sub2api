# Probe cooldown validation

The codex-buddy probe command now reuses completed per-model results for seven days within the existing provider/endpoint/header fingerprint. Success, failed requests, timeouts, fallback and unverified-identity results all consume the cooldown. Reused results retain their original timestamps and health classification. `--force` explicitly bypasses the cooldown. Interrupted whole-run reports still supply completed model results; unfinished model results are retried. Specialized model skips are reclassified without inference.

Reports expose `reusedCount`, `probedCount`, `reusedIds`, `cooldownDays` and per-model `nextProbeAt`; human progress distinguishes reuse from inference and prints the next due time. Exact OK text and exact function arguments remain mandatory, as does concrete model identity; a fallback remains fallback. Prompts and function descriptions were shortened. Models explicitly supporting reasoning `none` use a 128-token ceiling; others retain 512 tokens to accommodate reasoning. An incomplete response still fails.

Validation used only local HTTP fixtures; no new paid inference requests were made:

- `npm run build && node --test test/probe.test.mjs`: 14 passed.
- `npm test`: 100 total, 97 passed, 3 skipped, 0 failed.
- Read-only evaluation of the existing 331-model provider report: 307 completed results are reusable, 24 were specialized skips. The completed results next become due between 2026-10-06T12:08:55.200Z and 2026-10-06T12:16:52.281Z (UTC). The report was not changed by this read-only check.

No schedule, production credentials, route policy or deployment was changed.
