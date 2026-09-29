package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectModelHealthProbeCandidatesPrioritizesUnknownModelsPerAccount(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)
	accounts := []Account{
		modelHealthProbeAccount(180, []string{"nvidia/embed-qa-4", "zeta", "alpha"}),
		modelHealthProbeAccount(287, []string{"agnes-video-v2.0", "agnes-2.5-flash", "agnes-2.0-flash"}),
	}
	states := []AccountModelHealthState{
		{AccountID: 180, Model: "alpha", LastSuccessAt: &old},
		{AccountID: 287, Model: "agnes-2.0-flash", LastSuccessAt: &old},
	}

	got := collectModelHealthProbeCandidates(accounts, states, now, 3)
	require.Equal(t, []modelHealthProbeCandidate{
		{AccountID: 180, Model: "zeta", CheckedAt: nil},
		{AccountID: 287, Model: "agnes-2.5-flash", CheckedAt: nil},
		{AccountID: 180, Model: "alpha", CheckedAt: &old},
	}, got)
}

func TestCollectModelHealthProbeCandidatesWaitsAfterFailure(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	recentFailure := now.Add(-time.Hour)
	account := modelHealthProbeAccount(287, []string{"agnes-2.0-flash", "agnes-2.5-flash"})

	got := collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 287, Model: "agnes-2.0-flash", LastFailureAt: &recentFailure,
	}}, now, 10)

	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 287, Model: "agnes-2.5-flash"}}, got,
		"an untested model remains due even when another model recently failed")
	got = collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 287, Model: "agnes-2.0-flash", LastFailureAt: &recentFailure,
	}}, now.Add(7*24*time.Hour), 10)
	require.Equal(t, []modelHealthProbeCandidate{
		{AccountID: 287, Model: "agnes-2.5-flash"},
		{AccountID: 287, Model: "agnes-2.0-flash", CheckedAt: &recentFailure},
	}, got)
}

func TestModelProbePolicySkipsDisabledAndGPTModels(t *testing.T) {
	account := modelHealthProbeAccount(283, []string{"gpt-6-astra", "openai/GPT-5.5", "chatgpt-4o-latest", "alias", "agnes-3.0-flash"})
	account.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "gpt-5.5"}}
	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 283, Model: "agnes-3.0-flash"}},
		collectModelHealthProbeCandidates([]Account{account}, nil, time.Now(), 10))
	account.Extra[ModelHealthProbeEnabledKey] = false
	require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, nil, time.Now(), 10))
	require.False(t, account.allowsAutomaticModelProbe("agnes-3.0-flash"))
}

func TestModelProbeIntervalUsesAccountConfigurationAndRealTraffic(t *testing.T) {
	now := time.Now()
	last := now.Add(-48 * time.Hour)
	account := modelHealthProbeAccount(198, []string{"agnes-3.0-flash", "never-called"})
	states := []AccountModelHealthState{{AccountID: 198, Model: "agnes-3.0-flash", LastSuccessAt: &last}}
	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 198, Model: "never-called"}},
		collectModelHealthProbeCandidates([]Account{account}, states, now, 10),
		"probe cadence is tracked per model, so an untested catalog entry is due")
	account.Extra[ModelHealthProbeIntervalKey] = float64(24)
	require.Equal(t, []modelHealthProbeCandidate{
		{AccountID: 198, Model: "never-called"},
		{AccountID: 198, Model: "agnes-3.0-flash", CheckedAt: &last},
	},
		collectModelHealthProbeCandidates([]Account{account}, states, now, 10))
	account.Extra[ModelHealthProbeIntervalKey] = float64(-1)
	require.Equal(t, 168*time.Hour, account.ModelProbePolicy().Interval)
}

func TestModelHealthProbeCovers331ModelsWithoutRepeatingRecentOutcomes(t *testing.T) {
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	models := make([]string, 331)
	for i := range models {
		models[i] = fmt.Sprintf("vendor/model-%03d", i)
	}
	accounts := []Account{modelHealthProbeAccount(1, models)}
	states := make([]AccountModelHealthState, 0, len(models))
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
	// 成功和失败均推进队列，331 个账号/模型对可在一天内完成首次轮询。
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
	got := collectModelHealthProbeCandidates([]Account{account}, nil, time.Now(), 10)
	require.Len(t, got, 2)
	require.Equal(t, "configured-only", got[0].Model)
	require.Equal(t, "shared", got[1].Model)
	delete(account.Extra, UpstreamSupportedModelsExtraKey)
	require.Equal(t, got, collectModelHealthProbeCandidates([]Account{account}, nil, time.Now(), 10))
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
	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 9, Model: "wire-model"}},
		collectModelHealthProbeCandidates([]Account{account}, nil, time.Now(), 10))
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
