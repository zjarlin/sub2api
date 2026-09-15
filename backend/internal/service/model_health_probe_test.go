package service

import (
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
	}, got)
}

func TestCollectModelHealthProbeCandidatesWaitsAfterFailure(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	recentFailure := now.Add(-time.Hour)
	account := modelHealthProbeAccount(287, []string{"agnes-2.0-flash", "agnes-2.5-flash"})

	got := collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 287, Model: "agnes-2.0-flash", LastFailureAt: &recentFailure,
	}}, now, 10)

	require.Empty(t, got, "a failed test also consumes the account probe interval")
	got = collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 287, Model: "agnes-2.0-flash", LastFailureAt: &recentFailure,
	}}, now.Add(7*24*time.Hour), 10)
	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 287, Model: "agnes-2.5-flash"}}, got)
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
	require.Empty(t, collectModelHealthProbeCandidates([]Account{account}, states, now, 10))
	account.Extra[ModelHealthProbeIntervalKey] = float64(24)
	require.Len(t, collectModelHealthProbeCandidates([]Account{account}, states, now, 10), 1)
	account.Extra[ModelHealthProbeIntervalKey] = float64(-1)
	require.Equal(t, 168*time.Hour, account.ModelProbePolicy().Interval)
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
