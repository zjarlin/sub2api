package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type modelProbeRecoveryRepo struct {
	AccountRepository
	account  *Account
	model    string
	observed any
	err      error
}

func (r *modelProbeRecoveryRepo) ClearUnsupportedModelIfObserved(_ context.Context, account *Account, model string, observed any) (bool, error) {
	r.account, r.model, r.observed = account, model, observed
	return r.err == nil, r.err
}

func TestRecoverProbedModelUsesObservedMappedFailure(t *testing.T) {
	observation := map[string]any{"status_code": 404, "detected_at": "2026-09-28T00:00:00Z"}
	account := &Account{
		ID: 42, Platform: PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"public": "upstream", "upstream": "different-model"}},
		Extra:       map[string]any{UnsupportedModelsExtraKey: map[string]any{"upstream": observation, "other": observation}},
	}
	for _, model := range []string{"public", "upstream"} {
		t.Run(model, func(t *testing.T) {
			repo := &modelProbeRecoveryRepo{}
			svc := &UpstreamModelRefreshService{accountRepo: repo}
			require.NoError(t, svc.recoverProbedModel(context.Background(), account, model))
			require.Same(t, account, repo.account)
			require.Equal(t, "upstream", repo.model)
			require.Equal(t, observation, repo.observed)
			require.Len(t, account.Extra[UnsupportedModelsExtraKey], 2)
		})
	}
}

func TestRecoverProbedModelSkipsUnobservedFailuresAndPropagatesErrors(t *testing.T) {
	repo := &modelProbeRecoveryRepo{err: errors.New("write unavailable")}
	svc := &UpstreamModelRefreshService{accountRepo: repo}
	account := &Account{ID: 42}
	require.NoError(t, svc.recoverProbedModel(context.Background(), nil, "model"))
	require.NoError(t, svc.recoverProbedModel(context.Background(), account, "model"))
	require.Nil(t, repo.account)
	account.Extra = map[string]any{UnsupportedModelsExtraKey: map[string]any{"model": map[string]any{"status_code": 404}}}
	require.ErrorIs(t, svc.recoverProbedModel(context.Background(), account, "model"), repo.err)

	svc.accountRepo = &upstreamModelRefreshRepoStub{}
	require.NoError(t, svc.recoverProbedModel(context.Background(), account, "model"))
}
