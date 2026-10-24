package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func exhaustionTestAccount(id int64, enabled bool) Account {
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Schedulable: true, Concurrency: 2, Priority: 99, GroupIDs: []int64{71},
		Credentials: map[string]any{"model_mapping": testModelMapping("glm", "qwen")},
		Extra:       map[string]any{AccountExhaustionExtraKey: AccountExhaustionPolicy{Enabled: enabled, Models: []string{"glm", "qwen"}}}}
}

func TestExhaustionPolicyValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		policy   any
		platform string
		valid    bool
	}{
		{"valid", AccountExhaustionPolicy{Enabled: true, Models: []string{"glm", "qwen"}, GroupIDs: []int64{71}}, PlatformOpenAI, true},
		{"empty", AccountExhaustionPolicy{Enabled: true}, PlatformOpenAI, false},
		{"duplicate", AccountExhaustionPolicy{Enabled: true, Models: []string{"glm", "glm"}}, PlatformOpenAI, false},
		{"unknown model", AccountExhaustionPolicy{Enabled: true, Models: []string{"other"}}, PlatformOpenAI, false},
		{"foreign group", AccountExhaustionPolicy{Enabled: true, Models: []string{"glm"}, GroupIDs: []int64{72}}, PlatformOpenAI, false},
		{"unsupported platform", AccountExhaustionPolicy{Enabled: true, Models: []string{"glm"}}, PlatformAnthropic, false},
		{"malformed", map[string]any{"enabled": "yes"}, PlatformOpenAI, false},
		{"disabled retains removed model", AccountExhaustionPolicy{Enabled: false, Models: []string{"other"}}, PlatformOpenAI, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := exhaustionTestAccount(1, true)
			a.Platform = tc.platform
			a.Extra[AccountExhaustionExtraKey] = tc.policy
			err := ValidateAccountExhaustionPolicy(&a, []int64{71})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestExhaustionSchedulerPriorityOverflowAndRecovery(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	preferred := exhaustionTestAccount(1, true)
	other := exhaustionTestAccount(2, false)
	other.Priority = 0
	accounts := []Account{other, preferred}
	acquired := []int64{}
	slots := map[int64]bool{1: true, 2: true}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: slots, acquiredIDs: &acquired})}
	groupID := int64(71)
	selectAccount := func(ctx context.Context, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		return svc.SelectAccountWithScheduler(ctx, &groupID, "", "", model, nil, OpenAIUpstreamTransportAny, false)
	}
	selected, decision, err := selectAccount(context.Background(), "glm")
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	require.Equal(t, "account_exhaustion", decision.Layer)
	selected.ReleaseFunc()
	slots[1] = false
	selected, _, err = selectAccount(context.Background(), "glm")
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	require.True(t, selected.Acquired)
	require.Nil(t, selected.WaitPlan)
	selected.ReleaseFunc()
	slots[1] = true
	selected, _, err = selectAccount(context.Background(), "qwen")
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	selected.ReleaseFunc()
	require.Equal(t, []int64{1, 1, 2, 1}, acquired)
}

func TestExhaustionScopedCandidateCannotEscapeToOtherAccounts(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	accounts := []Account{exhaustionTestAccount(1, true), exhaustionTestAccount(2, false)}
	acquired := []int64{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true}, acquiredIDs: &acquired})}
	groupID := int64(71)
	ctx := WithAccountExhaustionRoute(context.Background(), 1, "glm")
	_, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "glm", nil, OpenAIUpstreamTransportAny, false)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	ctx = WithAccountExhaustionRoute(ctx, 1, "qwen")
	_, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "qwen", nil, OpenAIUpstreamTransportAny, false)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{1}, acquired, "账号并发已满时后续模型直接跳过")
	ctx = WithAccountExhaustionRoute(ctx, 0, "glm")
	selected, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "glm", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	selected.ReleaseFunc()
}

func TestExhaustionAttemptHistoryOnlyExcludesSameModel(t *testing.T) {
	ctx := WithAccountExhaustionRoute(context.Background(), 1, "glm")
	ctx = WithAccountExhaustionRoute(ctx, 1, "qwen")
	require.Contains(t, exhaustionExcludedAccounts(ctx, "glm", nil), int64(1))
	require.NotContains(t, exhaustionExcludedAccounts(ctx, "qwen", nil), int64(1))
	ctx = WithAccountExhaustionRoute(ctx, 0, "glm")
	require.Contains(t, exhaustionExcludedAccounts(ctx, "qwen", nil), int64(1))
	require.Empty(t, exhaustionExcludedAccounts(context.Background(), "glm", nil))
}

func TestExhaustionRespectsAccountCooldownAndHardBinding(t *testing.T) {
	a := exhaustionTestAccount(1, true)
	future := time.Now().Add(time.Hour)
	a.RateLimitResetAt = &future
	svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a}}}
	groupID := int64(71)
	req := OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "glm", RequiredTransport: OpenAIUpstreamTransportAny}
	_, _, handled, err := svc.selectExhaustionAccount(WithAccountExhaustionRoute(context.Background(), 1, "glm"), req)
	require.True(t, handled)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	req.PreviousResponseID = "response-owned"
	_, _, handled, err = svc.selectExhaustionAccount(context.Background(), req)
	require.NoError(t, err)
	require.False(t, handled)
	req.PreviousResponseID = ""
	req.GuardianParentAccountID = 2
	_, _, handled, err = svc.selectExhaustionAccount(context.Background(), req)
	require.NoError(t, err)
	require.False(t, handled)
}

type exhaustionErrorCache struct{ schedulerTestConcurrencyCache }

func (exhaustionErrorCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return false, errors.New("redis unavailable")
}
func TestExhaustionDoesNotTreatRedisErrorsAsFull(t *testing.T) {
	a := exhaustionTestAccount(1, true)
	groupID := int64(71)
	svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a}}, concurrencyService: NewConcurrencyService(exhaustionErrorCache{})}
	_, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "glm", nil, OpenAIUpstreamTransportAny, false)
	require.ErrorContains(t, err, "redis unavailable")
	require.NotErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestExhaustionModelCooldownDoesNotDisableSiblings(t *testing.T) {
	a := exhaustionTestAccount(1, true)
	a.Credentials["base_url"] = "https://first.example"
	repo := &modelNotFoundAccountRepoStub{}
	limiter := &RateLimitService{accountRepo: repo}
	require.True(t, limiter.handleModelAlias429(context.Background(), &a, 429, http.Header{"Retry-After": []string{"90"}}, []byte(`{"error":{"message":"model rate limited"}}`), "glm"))
	require.Len(t, repo.modelRateLimitCalls, 1)
	call := repo.modelRateLimitCalls[0]
	setAccountModelRateLimitSnapshot(&a, call.scope, call.resetAt, "test", time.Now())
	require.False(t, a.IsSchedulableForModelWithContext(context.Background(), "glm"))
	require.True(t, a.IsSchedulableForModelWithContext(context.Background(), "qwen"))
	require.Nil(t, a.RateLimitResetAt)
	// 关闭优先策略不抹掉真实限流窗口；换基础地址不污染新的路由。
	a.Extra[AccountExhaustionExtraKey] = AccountExhaustionPolicy{Enabled: false, Models: []string{"glm"}}
	require.False(t, a.IsSchedulableForModelWithContext(context.Background(), "glm"))
	a.Credentials["base_url"] = "https://second.example"
	require.True(t, a.IsSchedulableForModelWithContext(context.Background(), "glm"))
	require.False(t, limiter.handleModelAlias429(context.Background(), &a, 429, nil, []byte(`{"error":{"code":"insufficient_quota"}}`), "glm"))
}

func TestExhaustionRespectsGroupAndMultipleAccountOrder(t *testing.T) {
	a, b := exhaustionTestAccount(3, true), exhaustionTestAccount(2, true)
	a.Priority, b.Priority = 1, 1
	a.Extra[AccountExhaustionExtraKey] = AccountExhaustionPolicy{Enabled: true, Models: []string{"glm"}, GroupIDs: []int64{72}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}}
	groupID := int64(71)
	selected, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "glm", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, b.ID, selected.Account.ID)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
	accounts := []Account{a, b}
	sortExhaustionAccounts(accounts)
	require.Equal(t, b.ID, accounts[0].ID)
}
