//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type accountRepoStubForCopy struct {
	accountRepoStub
	sourceAccount *Account
	created       *Account
	createCalls   int
	bindCalls     int
}

func (s *accountRepoStubForCopy) GetByID(_ context.Context, id int64) (*Account, error) {
	if s.created != nil && id == s.created.ID {
		return s.created, nil
	}
	return s.sourceAccount, nil
}

func (s *accountRepoStubForCopy) Create(_ context.Context, account *Account) error {
	s.createCalls++
	cloned := *account
	cloned.ID = 9001
	cloned.GroupIDs = append([]int64(nil), account.GroupIDs...)
	cloned.Credentials = cloneJSONMap(account.Credentials)
	cloned.Extra = cloneJSONMap(account.Extra)
	s.created = &cloned
	account.ID = cloned.ID
	return nil
}

func (s *accountRepoStubForCopy) BindGroups(_ context.Context, accountID int64, groupIDs []int64) error {
	s.bindCalls++
	if s.created != nil && s.created.ID == accountID {
		s.created.GroupIDs = append([]int64(nil), groupIDs...)
	}
	return nil
}

func TestCopyAccount_ClonesConfigAndClearsRuntimeExtra(t *testing.T) {
	expiresAt := time.Unix(1760000000, 0).UTC()
	rateMultiplier := 1.75
	loadFactor := 7
	proxyID := int64(33)
	notes := "source notes"
	repo := &accountRepoStubForCopy{
		sourceAccount: &Account{
			ID:                 101,
			Name:               "source-account",
			Notes:              &notes,
			Platform:           PlatformOpenAI,
			Type:               AccountTypeAPIKey,
			Credentials:        map[string]any{"api_key": "sk-test", "nested": map[string]any{"x": "y"}},
			Extra:              map[string]any{"privacy_mode": PrivacyModeTrainingOff, "quota_limit": 10.0, "quota_used": 4.5, "quota_daily_used": 2.0, "quota_daily_start": "2026-05-17T00:00:00Z", "quota_daily_reset_at": "2026-05-18T00:00:00Z", "quota_weekly_used": 3.0, "quota_weekly_start": "2026-05-12T00:00:00Z", "quota_weekly_reset_at": "2026-05-19T00:00:00Z", "model_rate_limits": map[string]any{"gpt-5": map[string]any{"rate_limit_reset_at": "2099-01-01T00:00:00Z"}}, "antigravity_credits_overages": map[string]any{"AICredits": true}, "antigravity_quota_scopes": map[string]any{"scope": true}, "codex_usage_updated_at": "2026-05-17T10:00:00Z", "codex_primary_tokens": 12, "passive_usage_total_requests": 8},
			ProxyID:            &proxyID,
			Concurrency:        5,
			Priority:           12,
			RateMultiplier:     &rateMultiplier,
			LoadFactor:         &loadFactor,
			ExpiresAt:          &expiresAt,
			AutoPauseOnExpired: true,
			GroupIDs:           []int64{7, 8},
		},
	}

	svc := &adminServiceImpl{accountRepo: repo}
	copied, err := svc.CopyAccount(context.Background(), repo.sourceAccount.ID)

	require.NoError(t, err)
	require.NotNil(t, copied)
	require.Equal(t, int64(9001), copied.ID)
	require.Equal(t, "source-account_copy", copied.Name)
	require.Equal(t, []int64{7, 8}, copied.GroupIDs)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 1, repo.bindCalls)
	require.NotNil(t, repo.created)
	require.Equal(t, "sk-test", repo.created.Credentials["api_key"])
	require.Equal(t, PrivacyModeTrainingOff, repo.created.Extra["privacy_mode"])
	require.Equal(t, 10.0, repo.created.Extra["quota_limit"])
	require.NotContains(t, repo.created.Extra, "quota_used")
	require.NotContains(t, repo.created.Extra, "quota_daily_used")
	require.NotContains(t, repo.created.Extra, "quota_daily_start")
	require.NotContains(t, repo.created.Extra, "quota_daily_reset_at")
	require.NotContains(t, repo.created.Extra, "quota_weekly_used")
	require.NotContains(t, repo.created.Extra, "quota_weekly_start")
	require.NotContains(t, repo.created.Extra, "quota_weekly_reset_at")
	require.NotContains(t, repo.created.Extra, "model_rate_limits")
	require.NotContains(t, repo.created.Extra, "antigravity_credits_overages")
	require.NotContains(t, repo.created.Extra, "antigravity_quota_scopes")
	require.NotContains(t, repo.created.Extra, "codex_usage_updated_at")
	require.NotContains(t, repo.created.Extra, "codex_primary_tokens")
	require.NotContains(t, repo.created.Extra, "passive_usage_total_requests")

	nested := repo.created.Credentials["nested"].(map[string]any)
	nested["x"] = "changed"
	originalNested := repo.sourceAccount.Credentials["nested"].(map[string]any)
	require.Equal(t, "y", originalNested["x"])
}

func TestCopyAccount_UngroupedSourceStaysUngrouped(t *testing.T) {
	repo := &accountRepoStubForCopy{
		sourceAccount: &Account{
			ID:          202,
			Name:        "ungrouped",
			Platform:    PlatformAnthropic,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "token"},
			Extra:       map[string]any{},
		},
	}

	svc := &adminServiceImpl{accountRepo: repo}
	copied, err := svc.CopyAccount(context.Background(), repo.sourceAccount.ID)

	require.NoError(t, err)
	require.NotNil(t, copied)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 0, repo.bindCalls)
	require.Empty(t, repo.created.GroupIDs)
}
