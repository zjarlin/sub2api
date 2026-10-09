package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectModelHealthProbeCandidatesOnlyRechecksObservedModels(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)
	accounts := []Account{
		modelHealthProbeAccount(180, []string{"nvidia/embed-qa-4", "zeta", "alpha"}),
		modelHealthProbeAccount(181, []string{"nvidia/esmfold", "nvidia/ai-synthetic-video-detector"}),
		modelHealthProbeAccount(287, []string{"agnes-video-v2.0", "agnes-2.5-flash", "agnes-2.0-flash"}),
	}
	states := []AccountModelHealthState{
		{AccountID: 180, Model: "alpha", LastSuccessAt: &old},
		{AccountID: 180, Model: "nvidia/embed-qa-4", LastSuccessAt: &old},
		{AccountID: 181, Model: "nvidia/esmfold", LastSuccessAt: &old},
		{AccountID: 181, Model: "nvidia/ai-synthetic-video-detector", LastSuccessAt: &old},
		{AccountID: 287, Model: "agnes-2.0-flash", LastSuccessAt: &old},
	}

	got := collectModelHealthProbeCandidates(accounts, states, now, 3)
	require.Equal(t, []modelHealthProbeCandidate{
		{AccountID: 180, Model: "alpha", CheckedAt: &old},
		{AccountID: 287, Model: "agnes-2.0-flash", CheckedAt: &old},
	}, got)
}

func TestCollectModelHealthProbeCandidatesWaitsAfterFailure(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	recentFailure := now.Add(-time.Hour)
	account := modelHealthProbeAccount(287, []string{"agnes-2.0-flash", "agnes-2.5-flash"})

	got := collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 287, Model: "agnes-2.0-flash", LastFailureAt: &recentFailure,
	}}, now, 10)

	require.Empty(t, got, "unobserved catalog models do not trigger paid automatic probes")
	got = collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 287, Model: "agnes-2.0-flash", LastFailureAt: &recentFailure,
	}}, now.Add(7*24*time.Hour), 10)
	require.Equal(t, []modelHealthProbeCandidate{
		{AccountID: 287, Model: "agnes-2.0-flash", CheckedAt: &recentFailure},
	}, got)
}

func TestModelProbePolicySkipsDisabledAndGPTModels(t *testing.T) {
	account := modelHealthProbeAccount(283, []string{"gpt-6-astra", "openai/GPT-5.5", "chatgpt-4o-latest", "alias", "agnes-3.0-flash"})
	account.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "gpt-5.5"}}
	old := time.Now().Add(-8 * 24 * time.Hour)
	states := []AccountModelHealthState{{AccountID: 283, Model: "agnes-3.0-flash", LastSuccessAt: &old}}
	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 283, Model: "agnes-3.0-flash", CheckedAt: &old}},
		collectModelHealthProbeCandidates([]Account{account}, states, time.Now(), 10))
	account.Extra[ModelHealthProbeEnabledKey] = false
	require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, states, time.Now(), 10))
	require.False(t, account.allowsAutomaticModelProbe("agnes-3.0-flash"))
}

func TestModelProbeIntervalUsesAccountConfigurationAndRealTraffic(t *testing.T) {
	now := time.Now()
	last := now.Add(-48 * time.Hour)
	account := modelHealthProbeAccount(198, []string{"agnes-3.0-flash", "never-called"})
	states := []AccountModelHealthState{{AccountID: 198, Model: "agnes-3.0-flash", LastSuccessAt: &last}}
	require.Empty(t,
		collectModelHealthProbeCandidates([]Account{account}, states, now, 10),
		"probe cadence is tracked only for models with a prior health record")
	account.Extra[ModelHealthProbeIntervalKey] = float64(24)
	require.Empty(t,
		collectModelHealthProbeCandidates([]Account{account}, states, now, 10),
		"short account settings cannot bypass the minimum seven-day cooldown")
	account.Extra[ModelHealthProbeIntervalKey] = float64(-1)
	require.Equal(t, 168*time.Hour, account.ModelProbePolicy().Interval)
}

func TestModelHealthProbeRechecks331ObservedModelsWithoutRepeatingRecentOutcomes(t *testing.T) {
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	models := make([]string, 331)
	for i := range models {
		models[i] = fmt.Sprintf("vendor/model-%03d", i)
	}
	accounts := []Account{modelHealthProbeAccount(1, models)}
	states := make([]AccountModelHealthState, 0, len(models))
	for _, model := range models {
		checked := start.Add(-8 * 24 * time.Hour)
		states = append(states, AccountModelHealthState{AccountID: 1, Model: model, LastSuccessAt: &checked})
	}
	seen := make(map[string]bool)
	now := start
	for round := 0; round < 34; round++ {
		batch := collectModelHealthProbeCandidates(accounts, states, now, modelHealthProbeLimit)
		require.NotEmpty(t, batch)
		require.LessOrEqual(t, len(batch), modelHealthProbeLimit)
		for _, candidate := range batch {
			require.False(t, seen[candidate.Model], candidate.Model)
			seen[candidate.Model] = true
			checked := now
			state := AccountModelHealthState{AccountID: candidate.AccountID, Model: candidate.Model}
			if len(states)%2 == 0 {
				state.LastSuccessAt = &checked
			} else {
				state.LastFailureAt = &checked
			}
			states = append(states, state)
		}
		now = now.Add(modelHealthProbeInterval)
	}
	// 成功和失败均推进已验证模型的队列，目录新模型不会进入自动首轮扫测。
	require.Len(t, seen, len(models))
	require.Less(t, now.Sub(start), 24*time.Hour)
	require.Empty(t, collectModelHealthProbeCandidates(accounts, states, now, modelHealthProbeLimit))
	require.Len(t, collectModelHealthProbeCandidates(accounts, states, now.Add(7*24*time.Hour), modelHealthProbeLimit), modelHealthProbeLimit)
}

func TestModelHealthProbeIncludesConfiguredTargetsAndDeduplicatesCatalog(t *testing.T) {
	account := modelHealthProbeAccount(7, []string{"shared", " shared ", "SHARED"})
	account.Credentials = map[string]any{"model_mapping": map[string]any{
		"alias": "shared", "other-alias": "configured-only", "wildcard": "vendor/*",
	}}
	old := time.Now().Add(-8 * 24 * time.Hour)
	states := []AccountModelHealthState{
		{AccountID: 7, Model: "configured-only", LastSuccessAt: &old},
		{AccountID: 7, Model: "shared", LastSuccessAt: &old},
	}
	got := collectModelHealthProbeCandidates([]Account{account}, states, time.Now(), 10)
	require.Len(t, got, 2)
	require.Equal(t, "configured-only", got[0].Model)
	require.Equal(t, "shared", got[1].Model)
	delete(account.Extra, UpstreamSupportedModelsExtraKey)
	require.Equal(t, got, collectModelHealthProbeCandidates([]Account{account}, states, time.Now(), 10))
}

func TestModelHealthProbeUsesNewestAliasAndCaseObservation(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Hour)
	old := now.Add(-8 * 24 * time.Hour)
	account := modelHealthProbeAccount(9, []string{"actual"})
	account.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "actual"}}
	for _, id := range []string{"ACTUAL", "alias"} {
		states := []AccountModelHealthState{
			{AccountID: 9, Model: id, LastSuccessAt: &recent},
			{AccountID: 9, Model: "actual", LastFailureAt: &old},
		}
		require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, states, now, 10), id)
		states[0], states[1] = states[1], states[0]
		require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, states, now, 10), id)
	}
}

func TestModelHealthProbePassthroughKeepsWireModelIdentity(t *testing.T) {
	account := modelHealthProbeAccount(9, nil)
	account.Platform, account.Type = PlatformOpenAI, AccountTypeAPIKey
	account.Extra["openai_passthrough"] = true
	account.Credentials = map[string]any{"model_mapping": map[string]any{"wire-model": "unused-target"}}
	old := time.Now().Add(-8 * 24 * time.Hour)
	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 9, Model: "wire-model", CheckedAt: &old}},
		collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{AccountID: 9, Model: "wire-model", LastSuccessAt: &old}}, time.Now(), 10))
}

func modelHealthProbeAccount(id int64, models []string) Account {
	return Account{
		ID:          id,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{UpstreamSupportedModelsExtraKey: UpstreamSupportedModelsSnapshot{
			Source:   "upstream",
			SyncedAt: time.Now().UTC().Format(time.RFC3339),
			Models:   models,
		}},
	}
}

func TestModelProbePolicyEnforcesSevenDayFloorAndPreservesLongerSettings(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  time.Duration
	}{
		{nil, 168 * time.Hour}, {24, 168 * time.Hour}, {float64(167), 168 * time.Hour},
		{168, 168 * time.Hour}, {float64(336), 336 * time.Hour}, {8760, 8760 * time.Hour},
		{-1, 168 * time.Hour}, {float64(8761), 168 * time.Hour},
	} {
		t.Run(fmt.Sprint(tc.value), func(t *testing.T) {
			account := modelHealthProbeAccount(1, nil)
			account.Extra[ModelHealthProbeIntervalKey] = tc.value
			require.Equal(t, tc.want, account.ModelProbePolicy().Interval)
		})
	}
}

func TestModelHealthProbeWaitsFullIntervalAfterSuccessFailureOrInterruptedAttempt(t *testing.T) {
	checked := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for _, outcome := range []string{"success", "failure", "interrupted"} {
		for _, hours := range []int{24, 168, 336} {
			t.Run(fmt.Sprintf("%s/%d", outcome, hours), func(t *testing.T) {
				account := modelHealthProbeAccount(1, []string{"model"})
				account.Extra[ModelHealthProbeIntervalKey] = hours
				state := AccountModelHealthState{AccountID: 1, Model: "model"}
				switch outcome {
				case "success":
					state.LastSuccessAt = &checked
				case "failure":
					state.LastFailureAt = &checked
				case "interrupted":
					state.LastProbeAt = &checked
				}
				interval := account.ModelProbePolicy().Interval
				require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{state}, checked.Add(interval-time.Nanosecond), 10))
				require.Len(t, collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{state}, checked.Add(interval), 10), 1)
			})
		}
	}
}

func TestModelHealthProbeCatalogAliasesShareAttemptCooldown(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Hour)
	old := now.Add(-8 * 24 * time.Hour)
	account := modelHealthProbeAccount(9, []string{"alias", "actual", "ACTUAL"})
	account.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "actual"}}
	require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, nil, now, 10))
	require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 9, Model: "actual", LastProbeAt: &recent,
	}}, now, 10))
	require.Len(t, collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 9, Model: "actual", LastProbeAt: &old,
	}}, now, 10), 1)
}

type modelProbeClaimGateRepo struct {
	AccountRepository
	account Account
	states  []AccountModelHealthState
	claims  int
	err     error
}

func (r *modelProbeClaimGateRepo) ListAccountModelHealthStates(context.Context) ([]AccountModelHealthState, error) {
	return r.states, nil
}

func (r *modelProbeClaimGateRepo) GetByID(context.Context, int64) (*Account, error) {
	return &r.account, nil
}

func (r *modelProbeClaimGateRepo) ClaimAccountModelProbe(context.Context, int64, string, time.Duration) (bool, error) {
	r.claims++
	return false, r.err
}

func TestModelHealthProbeDoesNotSendWithoutDurableClaim(t *testing.T) {
	for _, claimErr := range []error{nil, errors.New("database unavailable")} {
		old := time.Now().Add(-8 * 24 * time.Hour)
		repo := &modelProbeClaimGateRepo{account: modelHealthProbeAccount(1, []string{"model"}), states: []AccountModelHealthState{{AccountID: 1, Model: "model", LastSuccessAt: &old}}, err: claimErr}
		svc := &UpstreamModelRefreshService{accountRepo: repo}
		// 没有请求执行器；占用失败或已被其他实例占用必须在发送请求前返回。
		svc.probeDueModels(context.Background(), []Account{repo.account})
		require.Equal(t, 1, repo.claims)
	}
}
