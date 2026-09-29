//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func (s *AccountRepoSuite) TestModelProbeRecoveryPreservesOtherFailuresAndRejectsStaleSuccess() {
	for _, race := range []string{"none", "new_failure", "new_credentials"} {
		s.Run(race, func() {
			failure := service.UnsupportedModelObservation{DetectedAt: time.Now().UTC(), StatusCode: 404, Reason: "upstream_model_unsupported"}
			account := mustCreateAccount(s.T(), s.client, &service.Account{
				Name: "probe-recovery-" + race, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "observed"},
				Extra: map[string]any{service.UnsupportedModelsExtraKey: map[string]any{"restored": failure, "other": failure}, "unrelated": true},
			})
			observed, err := s.repo.GetByID(s.ctx, account.ID)
			s.Require().NoError(err)
			oldFailure := observed.Extra[service.UnsupportedModelsExtraKey].(map[string]any)["restored"]
			switch race {
			case "new_failure":
				failure.DetectedAt = failure.DetectedAt.Add(time.Second)
				s.Require().NoError(s.repo.SetUnsupportedModel(s.ctx, account.ID, "restored", failure))
			case "new_credentials":
				_, err = s.client.Account.UpdateOneID(account.ID).SetCredentials(map[string]any{"api_key": "replacement"}).Save(s.ctx)
				s.Require().NoError(err)
			}
			_, err = s.repo.sql.ExecContext(s.ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
			s.Require().NoError(err)
			cache := &schedulerCacheRecorder{}
			repo := newAccountRepositoryWithSQL(s.client, s.repo.sql, cache)
			cleared, err := repo.ClearUnsupportedModelIfObserved(s.ctx, observed, "restored", oldFailure)
			s.Require().NoError(err)
			s.Require().Equal(race == "none", cleared)
			got, err := s.repo.GetByID(s.ctx, account.ID)
			s.Require().NoError(err)
			remaining := got.Extra[service.UnsupportedModelsExtraKey].(map[string]any)
			s.Require().Contains(remaining, "other")
			s.Require().Equal(true, got.Extra["unrelated"])
			var outboxCount int
			rows, err := s.client.QueryContext(s.ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID)
			s.Require().NoError(err)
			s.Require().True(rows.Next())
			err = rows.Scan(&outboxCount)
			s.Require().NoError(rows.Close())
			s.Require().NoError(err)
			if race == "none" {
				s.Require().NotContains(remaining, "restored")
				s.Require().Equal(1, outboxCount)
				s.Require().Len(cache.setAccounts, 1)
				return
			}
			s.Require().Contains(remaining, "restored")
			s.Require().Zero(outboxCount)
			s.Require().Empty(cache.setAccounts)
		})
	}
}

func TestModelProbeRecoveryRollsBackWhenOutboxFails(t *testing.T) {
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{
		Name: "probe-recovery-atomic-outbox", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "observed"},
		Extra: map[string]any{service.UnsupportedModelsExtraKey: map[string]any{"restored": map[string]any{"status_code": 404}}},
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_ = client.Account.DeleteOneID(account.ID).Exec(context.Background())
	})
	repo := newAccountRepositoryWithSQL(client, &failAtomicSchedulerOutboxSQLExecutor{sqlExecutor: integrationDB}, nil)
	cleared, err := repo.ClearUnsupportedModelIfObserved(context.Background(), account, "restored", map[string]any{"status_code": 404})
	require.Error(t, err)
	require.False(t, cleared)
	got, err := repo.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	require.True(t, got.IsModelKnownUnsupported("restored"))
}
