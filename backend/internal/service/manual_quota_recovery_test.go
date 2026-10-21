//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newManualQuotaRecoveryService(account *Account) (*RateLimitService, *rateLimitAccountRepoStub) {
	repo := &rateLimitAccountRepoStub{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{account.ID: account},
		},
	}
	return &RateLimitService{accountRepo: repo}, repo
}

// 额度恢复日期必须落成账号级临时停调：日期前调度器跳过，到期后自动恢复。
func TestSetManualQuotaRecovery_WritesAccountWideTempUnschedulable(t *testing.T) {
	account := &Account{ID: 832, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	svc, repo := newManualQuotaRecoveryService(account)
	until := time.Now().Add(72 * time.Hour)

	updated, err := svc.SetManualQuotaRecovery(context.Background(), account.ID, until)
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, account.ID, repo.lastTempID)
	require.WithinDuration(t, until.Truncate(time.Minute), repo.lastTempUntil, time.Second)

	var state TempUnschedState
	require.NoError(t, json.Unmarshal([]byte(repo.lastTempReason), &state))
	require.True(t, state.ManualQuotaRecovery)
	require.Equal(t, TempUnschedScopeAccount, state.Scope)
	require.Equal(t, repo.lastTempUntil.Unix(), state.UntilUnix)
	require.Equal(t, manualQuotaRecoveryReasonPrefix, state.MatchedKeyword)
}

// 过去时间和超远日期都属于输入错误，不能静默改写成“立即恢复”或长期静默停调。
func TestSetManualQuotaRecovery_RejectsOutOfRangeDates(t *testing.T) {
	account := &Account{ID: 833, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	svc, repo := newManualQuotaRecoveryService(account)

	_, err := svc.SetManualQuotaRecovery(context.Background(), account.ID, time.Now().Add(-time.Minute))
	require.Error(t, err)

	_, err = svc.SetManualQuotaRecovery(context.Background(), account.ID, time.Now().Add(400*24*time.Hour))
	require.Error(t, err)

	require.Zero(t, repo.tempCalls, "非法日期不得写入调度状态")
}

// 永久禁用/未启用调度的账号不能借“恢复日期”绕过停用决策。
func TestSetManualQuotaRecovery_RequiresActiveAccount(t *testing.T) {
	for _, status := range []string{StatusError, StatusDisabled} {
		account := &Account{ID: 834, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: status, Schedulable: true}
		svc, repo := newManualQuotaRecoveryService(account)

		_, err := svc.SetManualQuotaRecovery(context.Background(), account.ID, time.Now().Add(24*time.Hour))
		require.Error(t, err, "status=%s", status)
		require.Zero(t, repo.tempCalls)
	}
}

// 状态满的账号只要仍是 active 就可以设置；调度开关本身由 schedulable 字段决定，
// 这里不因 schedulable=false 拒绝，避免管理员无法给已暂停的额度账号排恢复日期。
func TestSetManualQuotaRecovery_AllowsPausedSchedulableToggle(t *testing.T) {
	account := &Account{ID: 835, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: false}
	svc, repo := newManualQuotaRecoveryService(account)

	_, err := svc.SetManualQuotaRecovery(context.Background(), account.ID, time.Now().Add(6*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, repo.tempCalls)
}
