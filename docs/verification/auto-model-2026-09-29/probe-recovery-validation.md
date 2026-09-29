# Per-model probe recovery validation

A successful automatic model probe now removes only the model's exact previously observed unsupported record. The repository compares the failure JSON and account credentials, platform, type and proxy ID before clearing the JSONB path. Other model failures and unrelated account state remain intact. A newer failure or credential change prevents stale recovery. The database mutation and scheduler outbox insert commit atomically, followed by the existing detached scheduler cache refresh.

Validation:

- `go test ./internal/service ./internal/repository -run 'TestRecoverProbedModel|TestClearUnsupportedModelIfObserved' -count=1` passed.
- Targeted PostgreSQL integration tests passed: normal recovery preserves other failures, newer failure and credential rotation block recovery, scheduler cache/outbox update only after a successful compare-and-set, outbox failure rolls back recovery.
- Integration command used a temporary Go overlay at `/tmp/auto-recovery-integration-Qmh4KT/overlay.json` to supply two missing optional arguments to six existing `ListWithFilters` test calls. The unchanged baseline integration package otherwise does not compile: `account_repo_integration_test.go:631,654,718` and `account_repo_sort_integration_test.go:33,65,133`. These unrelated tracked tests were not edited.
- Integration command: `go test -overlay /tmp/auto-recovery-integration-Qmh4KT/overlay.json -tags integration ./internal/repository -run 'TestAccountRepoSuite/TestModelProbeRecovery|TestModelProbeRecoveryRollsBack' -count=1`.

This validates local source. No production configuration or deployment was changed by this recovery subtask.
