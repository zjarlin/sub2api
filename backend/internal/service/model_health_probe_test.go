package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectModelHealthProbeCandidatesPrioritizesUnknownModelsPerAccount(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	old := now.Add(-48 * time.Hour)
	accounts := []Account{
		modelHealthProbeAccount(180, []string{"nvidia/embed-qa-4", "zeta", "alpha"}),
		modelHealthProbeAccount(287, []string{"agnes-video-v2.0", "agnes-2.5-flash", "agnes-2.0-flash"}),
	}
	states := []AccountModelHealthState{
		{AccountID: 180, Model: "alpha", LastSuccessAt: &old},
		{AccountID: 287, Model: "agnes-2.0-flash", LastSuccessAt: &recent},
	}

	got := collectModelHealthProbeCandidates(accounts, states, now, 3)
	require.Equal(t, []modelHealthProbeCandidate{
		{AccountID: 180, Model: "zeta", CheckedAt: nil},
		{AccountID: 287, Model: "agnes-2.5-flash", CheckedAt: nil},
		{AccountID: 180, Model: "alpha", CheckedAt: &old},
	}, got)
}

func TestCollectModelHealthProbeCandidatesRotatesAfterFailure(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	recentFailure := now.Add(-time.Hour)
	account := modelHealthProbeAccount(287, []string{"agnes-2.0-flash", "agnes-2.5-flash"})

	got := collectModelHealthProbeCandidates([]Account{account}, []AccountModelHealthState{{
		AccountID: 287, Model: "agnes-2.0-flash", LastFailureAt: &recentFailure,
	}}, now, 10)

	require.Equal(t, []modelHealthProbeCandidate{{AccountID: 287, Model: "agnes-2.5-flash"}}, got)
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
