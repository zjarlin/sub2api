package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingAccountModelProbe struct {
	successByAccount map[int64]bool
	calls            []recordingAccountModelProbeCall
}

type recordingAccountModelProbeCall struct {
	accountID int64
	modelID   string
	mode      string
}

func (p *recordingAccountModelProbe) ProbeAccountModel(ctx context.Context, accountID int64, modelID string, mode string) (*ScheduledTestResult, error) {
	p.calls = append(p.calls, recordingAccountModelProbeCall{
		accountID: accountID,
		modelID:   modelID,
		mode:      mode,
	})
	if p.successByAccount != nil && p.successByAccount[accountID] {
		return &ScheduledTestResult{Status: "success"}, nil
	}
	return &ScheduledTestResult{Status: "failed", ErrorMessage: "probe failed"}, nil
}

type mutableRecoveryAccountRepo struct {
	AccountRepository
	accounts             []Account
	setSchedulableCalls  []int64
	clearTempCalls       []int64
	clearRateLimitCalls  []int64
	clearModelLimitCalls []int64
}

func (r *mutableRecoveryAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, errors.New("account not found")
}

func (r *mutableRecoveryAccountRepo) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	result := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Status == StatusActive && accountBelongsToGroupForRecovery(&account, groupID) {
			result = append(result, account)
		}
	}
	return result, nil
}

func (r *mutableRecoveryAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	return r.listSchedulableByGroupAndPlatforms(groupID, map[string]struct{}{platform: {}})
}

func (r *mutableRecoveryAccountRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	return r.listSchedulableByGroupAndPlatforms(groupID, platformSet)
}

func (r *mutableRecoveryAccountRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return r.listSchedulableByGroupAndPlatforms(0, map[string]struct{}{platform: {}})
}

func (r *mutableRecoveryAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *mutableRecoveryAccountRepo) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	return r.listSchedulableByGroupAndPlatforms(0, platformSet)
}

func (r *mutableRecoveryAccountRepo) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	return r.ListSchedulableByPlatforms(ctx, platforms)
}

func (r *mutableRecoveryAccountRepo) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	r.setSchedulableCalls = append(r.setSchedulableCalls, id)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].Schedulable = schedulable
			return nil
		}
	}
	return errors.New("account not found")
}

func (r *mutableRecoveryAccountRepo) ClearTempUnschedulable(ctx context.Context, id int64) error {
	r.clearTempCalls = append(r.clearTempCalls, id)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].TempUnschedulableUntil = nil
			r.accounts[i].TempUnschedulableReason = ""
			return nil
		}
	}
	return errors.New("account not found")
}

func (r *mutableRecoveryAccountRepo) ClearRateLimit(ctx context.Context, id int64) error {
	r.clearRateLimitCalls = append(r.clearRateLimitCalls, id)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].RateLimitedAt = nil
			r.accounts[i].RateLimitResetAt = nil
			r.accounts[i].OverloadUntil = nil
			return nil
		}
	}
	return errors.New("account not found")
}

func (r *mutableRecoveryAccountRepo) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	return nil
}

func (r *mutableRecoveryAccountRepo) ClearModelRateLimits(ctx context.Context, id int64) error {
	r.clearModelLimitCalls = append(r.clearModelLimitCalls, id)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			delete(r.accounts[i].Extra, "model_rate_limits")
			return nil
		}
	}
	return errors.New("account not found")
}

func (r *mutableRecoveryAccountRepo) listSchedulableByGroupAndPlatforms(groupID int64, platforms map[string]struct{}) ([]Account, error) {
	result := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if _, ok := platforms[account.Platform]; !ok {
			continue
		}
		if groupID > 0 && !accountBelongsToGroupForRecovery(&account, groupID) {
			continue
		}
		if account.IsSchedulable() {
			result = append(result, account)
		}
	}
	return result, nil
}

func accountBelongsToGroupForRecovery(account *Account, groupID int64) bool {
	if account == nil || groupID <= 0 {
		return true
	}
	for _, id := range account.GroupIDs {
		if id == groupID {
			return true
		}
	}
	for _, group := range account.Groups {
		if group != nil && group.ID == groupID {
			return true
		}
	}
	return len(account.GroupIDs) == 0 && len(account.Groups) == 0
}

func TestOpenAISelectAccountForModelWithExclusions_ProbeRecoversUnschedulableGroupAccount(t *testing.T) {
	groupID := int64(77)
	until := time.Now().Add(time.Hour)
	repo := &mutableRecoveryAccountRepo{
		accounts: []Account{
			{
				ID:                      101,
				Platform:                PlatformOpenAI,
				Type:                    AccountTypeAPIKey,
				Status:                  StatusActive,
				Schedulable:             false,
				TempUnschedulableUntil:  &until,
				TempUnschedulableReason: "stream disconnected",
				Concurrency:             1,
				Credentials:             map[string]any{"api_key": "sk-test"},
				GroupIDs:                []int64{groupID},
			},
		},
	}
	probe := &recordingAccountModelProbe{successByAccount: map[int64]bool{101: true}}
	svc := &OpenAIGatewayService{
		accountRepo:       repo,
		cache:             &stubGatewayCache{},
		accountModelProbe: probe,
	}

	account, err := svc.SelectAccountForModelWithExclusions(context.Background(), &groupID, "", "gpt-5.1", nil)

	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(101), account.ID)
	require.True(t, repo.accounts[0].Schedulable)
	require.Nil(t, repo.accounts[0].TempUnschedulableUntil)
	require.Equal(t, []int64{101}, repo.setSchedulableCalls)
	require.Equal(t, []int64{101}, repo.clearTempCalls)
	require.Len(t, probe.calls, 1)
	require.Equal(t, recordingAccountModelProbeCall{accountID: 101, modelID: "gpt-5.1", mode: AccountTestModeDefault}, probe.calls[0])
}
