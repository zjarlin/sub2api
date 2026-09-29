//go:build integration

package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelProbeClaimPersistsAcrossWorkersAndRestarts(t *testing.T) {
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{
		Name: "model-probe-claim", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "fixture"},
	})
	t.Cleanup(func() { _ = client.Account.DeleteOneID(account.ID).Exec(context.Background()) })
	ctx := context.Background()
	var claimed atomic.Int64
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
			ok, err := repo.ClaimAccountModelProbe(ctx, account.ID, " MODEL ", 24*time.Hour)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if ok {
				claimed.Add(1)
			}
		}()
	}
	workers.Wait()
	require.Equal(t, int64(1), claimed.Load())
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	ok, err := repo.ClaimAccountModelProbe(ctx, account.ID, "model", service.MinimumModelHealthProbeInterval)
	require.NoError(t, err)
	require.False(t, ok, "a new service instance must honor the persisted attempt even without an outcome")
	var success, failure *time.Time
	var attempt time.Time
	err = integrationDB.QueryRowContext(ctx, "SELECT last_success_at, last_failure_at, last_probe_at FROM account_model_health WHERE account_id=$1 AND model='model'", account.ID).Scan(&success, &failure, &attempt)
	require.NoError(t, err)
	require.Nil(t, success, "claiming must not fabricate a healthy result")
	require.Nil(t, failure)
	require.False(t, attempt.IsZero())
	health := newUsageLogRepositoryWithSQL(client, integrationDB)
	observations, err := health.ListModelHealthObservations(ctx, nil, service.PlatformOpenAI)
	require.NoError(t, err)
	for _, observation := range observations {
		require.NotEqual(t, account.ID, observation.AccountID, "an attempt alone must not be exposed as healthy")
	}

	_, err = integrationDB.ExecContext(ctx, "UPDATE account_model_health SET last_probe_at = CURRENT_TIMESTAMP - INTERVAL '169 hours' WHERE account_id=$1", account.ID)
	require.NoError(t, err)
	ok, err = repo.ClaimAccountModelProbe(ctx, account.ID, "model", 336*time.Hour)
	require.NoError(t, err)
	require.False(t, ok, "longer configured intervals must be retained")
	ok, err = repo.ClaimAccountModelProbe(ctx, account.ID, "model", service.MinimumModelHealthProbeInterval)
	require.NoError(t, err)
	require.True(t, ok)

	for _, outcome := range []string{"success", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			model := "recent-" + outcome
			recent := time.Now()
			if outcome == "success" {
				err = repo.RecordAccountModelHealthSuccess(ctx, account.ID, model, recent)
			} else {
				err = repo.RecordAccountModelHealthFailure(ctx, account.ID, model, recent)
			}
			require.NoError(t, err)
			ok, err := repo.ClaimAccountModelProbe(ctx, account.ID, model, service.MinimumModelHealthProbeInterval)
			require.NoError(t, err)
			require.False(t, ok, "recent health outcomes should avoid extra paid probes")
		})
	}
}
