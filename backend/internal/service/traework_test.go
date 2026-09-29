//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestTraeworkQuotaErrorMarksAccountUntilPoolReset(t *testing.T) {
	resetAt := time.Now().Add(12 * time.Hour).Truncate(time.Second)
	body := []byte(fmt.Sprintf(`{"error":{"code":4008,"message":"solo error code=4008 msg=Your requests have exceeded the quota.","type":"rate_limit_error","resets_at":%d}}`, resetAt.Unix()))
	for _, transport := range []string{"http", "stream"} {
		t.Run(transport, func(t *testing.T) {
			repo := &rateLimit429AccountRepoStub{}
			rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			blocker := &runtimeBlockRecorder{}
			rateLimits.SetAccountRuntimeBlocker(blocker)
			settings := NewSettingService(newMockSettingRepo(), &config.Config{})
			require.NoError(t, settings.SetRateLimit429CooldownSettings(context.Background(), &RateLimit429CooldownSettings{Enabled: false}))
			rateLimits.SetSettingService(settings)
			account := traeworkTestAccount()
			svc := &OpenAIGatewayService{rateLimitService: rateLimits}
			if transport == "stream" {
				failover := svc.newOpenAIStreamFailoverErrorWithModel(nil, account, false, "", body, "Your requests have exceeded the quota.", "deepseek-v4.1-flash")
				require.Equal(t, http.StatusTooManyRequests, failover.StatusCode)
			} else {
				svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, nil, body, "deepseek-v4.1-flash")
			}
			require.Equal(t, 1, repo.rateLimitCalls)
			require.Equal(t, account.ID, repo.lastRateLimitID)
			require.Equal(t, resetAt.Unix(), repo.lastRateLimitReset.Unix())
			require.Equal(t, []*Account{account}, blocker.accounts)
			require.Equal(t, []time.Time{resetAt}, blocker.until)
			require.Equal(t, StatusActive, account.Status)
		})
	}
}

func TestTraeworkQuotaErrorWithoutFuturePoolResetUsesFallback(t *testing.T) {
	for _, resetAt := range []string{"null", `"invalid"`, "0", "1"} {
		t.Run(resetAt, func(t *testing.T) {
			repo := &rateLimit429AccountRepoStub{}
			rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			body := []byte(fmt.Sprintf(`{"error":{"code":4008,"message":"Your requests have exceeded the quota.","resets_at":%s}}`, resetAt))
			before := time.Now()
			rateLimits.HandleUpstreamError(context.Background(), traeworkTestAccount(), http.StatusTooManyRequests, nil, body)
			require.Equal(t, 1, repo.rateLimitCalls)
			require.WithinRange(t, repo.lastRateLimitReset, before.Add(5*time.Second), time.Now().Add(5*time.Second))
		})
	}
}

func traeworkTestAccount() *Account {
	return &Account{ID: 601, Platform: PlatformTraework, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"base_url": "http://sub2api-traework:7864/v1", "api_key": "adapter-key", "api_protocol": APIProtocolChatCompletions}}
}

func TestTraeworkCredentialValidation(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := traeworkTestAccount()
	require.NoError(t, validateTraeworkCredentials(account.Platform, account.Type, account.Credentials))
	require.Error(t, validateTraeworkCredentials(account.Platform, AccountTypeOAuth, account.Credentials))
	account.Credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateTraeworkCredentials(account.Platform, account.Type, account.Credentials))
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.Empty(t, account.GetAccountMode())
}

func TestTraeworkRequiresBuiltinAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(nil)
	account := traeworkTestAccount()
	require.Error(t, validateTraeworkCredentials(account.Platform, account.Type, account.Credentials))
}

func TestTraeworkConnectionUsesBuiltinSidecar(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := traeworkTestAccount()
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorder := newTestContext()

	require.NoError(t, svc.TestAccountConnection(c, account.ID, "", "hi", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://sub2api-traework:7864/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer adapter-key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, DefaultTraeworkModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestTraeworkCreateAndUpdatePreserveConfiguredConcurrency(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
	svc := &adminServiceImpl{accountRepo: repo}
	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "traework", Platform: PlatformTraework, Type: AccountTypeAPIKey,
		Credentials: traeworkTestAccount().Credentials, SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, created.Concurrency)

	concurrency := 2
	updated, err := svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Concurrency: &concurrency})
	require.NoError(t, err)
	require.Equal(t, concurrency, updated.Concurrency)
	updated, err = svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Name: "renamed"})
	require.NoError(t, err)
	require.Equal(t, concurrency, updated.Concurrency)

	created, err = svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "parallel", Platform: PlatformTraework, Type: AccountTypeAPIKey,
		Credentials: traeworkTestAccount().Credentials, Concurrency: concurrency, SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Equal(t, concurrency, created.Concurrency)
}

func TestTraeworkBulkConcurrencyPreservesOtherAdapterLimits(t *testing.T) {
	for _, platform := range []string{PlatformTraework, PlatformDoubao, PlatformWorkbuddy, PlatformZcode} {
		t.Run(platform, func(t *testing.T) {
			account := traeworkTestAccount()
			account.Platform = platform
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			concurrency := 2
			_, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
				AccountIDs: []int64{account.ID}, Concurrency: &concurrency,
			})
			if platform != PlatformTraework {
				require.Error(t, err)
				require.Empty(t, repo.bulkUpdates)
				return
			}
			require.NoError(t, err)
			require.Len(t, repo.bulkUpdates, 1)
			require.Equal(t, concurrency, *repo.bulkUpdates[0].Concurrency)
		})
	}
}

func TestTraeworkAdapterProbeHasNoMaxTokens(t *testing.T) {
	body, err := providerTraeworkChatAdapter.buildBody(DefaultTraeworkModel, "hi")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "max_tokens").Exists())
	require.Equal(t, "choices.0.message.content", providerTraeworkChatAdapter.textPath)
}
