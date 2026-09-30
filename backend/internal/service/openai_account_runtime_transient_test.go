package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type transientCooldownAccountRepo struct {
	AccountRepository
}

func (transientCooldownAccountRepo) SetOverloaded(context.Context, int64, time.Time) error {
	return nil
}

func TestHandleOpenAITransientError_BlocksOnlyRequestedModel(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       5105,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}

	firstShouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, []byte(`{"error":{"message":"Upstream request failed","type":"upstream_error"}}`), "gpt-5.5")
	secondShouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, []byte(`{"error":{"message":"Upstream request failed","type":"upstream_error"}}`), "gpt-5.5")

	require.False(t, firstShouldDisable)
	require.False(t, secondShouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.6-terra"))
}

func TestHandleOpenAITransientError_TransientStatusesUseModelScope(t *testing.T) {
	for _, statusCode := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 520, 521, 522, 523, 524} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
			account := &Account{
				ID:       int64(5100 + statusCode),
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
			}

			firstShouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, statusCode, http.Header{}, []byte(`{"error":{"message":"temporary upstream failure"}}`), "gpt-5.5")
			secondShouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, statusCode, http.Header{}, []byte(`{"error":{"message":"temporary upstream failure"}}`), "gpt-5.5")

			require.False(t, firstShouldDisable)
			require.False(t, secondShouldDisable)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account), "status %d must not block the whole account", statusCode)
			require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"), "status %d should block the failing model", statusCode)
		})
	}
}

func TestHandleOpenAITransientError_529RemainsOverloadOnly(t *testing.T) {
	require.False(t, shouldCooldownOpenAITransientUpstreamError(529, []byte(`{"error":{"message":"overloaded"}}`)))
}

func TestCompatibleProviderTransientErrorsUseExistingModelCircuit(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek, PlatformZhipu, PlatformKimi, PlatformOpenCodeGo, PlatformGrok, PlatformCursor} {
		t.Run(platform, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
			account := &Account{ID: 5200, Platform: platform, Type: AccountTypeAPIKey}
			account.Credentials = map[string]any{"model_mapping": map[string]any{"public": "upstream", "upstream": "other"}}
			for range 2 {
				require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusServiceUnavailable, nil,
					[]byte(`{"error":{"message":"temporary upstream failure"}}`), "upstream"))
			}
			require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "public"))
			require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "upstream"), "模型映射只应用一次，不隔离其他模型")
			other := *account
			other.ID++
			require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(&other, "public"))
			svc.ReportOpenAIAccountScheduleResult(account, "upstream", true, nil)
			require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "public"), "真实成功清除同一模型的熔断")
		})
	}
}

func TestCompatibleProviderModelCircuitPreservesRetryPolicies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		platform    string
		accountType string
		status      int
		body        string
		pool        bool
	}{
		{"pool retry", PlatformOpenCodeGo, AccountTypeAPIKey, http.StatusServiceUnavailable, "temporary upstream failure", true},
		{"request capacity", PlatformOpenCodeGo, AccountTypeAPIKey, http.StatusServiceUnavailable, "server is overloaded", false},
		{"parameter error", PlatformOpenCodeGo, AccountTypeAPIKey, http.StatusBadRequest, "invalid parameter", false},
		{"oauth", PlatformGrok, AccountTypeOAuth, http.StatusServiceUnavailable, "temporary upstream failure", false},
		{"other protocol", PlatformAnthropic, AccountTypeAPIKey, http.StatusServiceUnavailable, "temporary upstream failure", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
			account := &Account{ID: 5300, Platform: tc.platform, Type: tc.accountType,
				Credentials: map[string]any{"pool_mode": tc.pool, "pool_mode_retry_status_codes": []any{float64(tc.status)}}}
			for range 2 {
				svc.handleOpenAIAccountUpstreamError(context.Background(), account, tc.status, nil, []byte(tc.body), "model")
			}
			require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "model"))
		})
	}
}

func TestHandleOpenAITransientError_CanonicalModelIsNotMappedTwice(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       5107,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"public-alias": "upstream-a",
				"upstream-a":   "upstream-b",
			},
		},
	}
	canonicalModel := account.GetMappedModel("public-alias")
	require.Equal(t, "upstream-a", canonicalModel)

	for range 2 {
		svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, []byte(`{"error":{"message":"temporary upstream failure"}}`), canonicalModel)
	}

	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "public-alias"))
	svc.ReportOpenAIAccountScheduleResult(account, canonicalModel, true, nil)
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "public-alias"))
}

func TestHandleOpenAITransientError_DoesNotBlockParameter400(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       5103,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}

	shouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, []byte(`{"error":{"message":"Invalid type for input[0].arguments"}}`), "gpt-5.5")

	require.False(t, shouldDisable)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"))
}

func TestHandleOpenAITransientError_HardDisableStillBlocksWholeAccount(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 5106, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "upstream_disable")

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.5"))
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-sol"))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}
