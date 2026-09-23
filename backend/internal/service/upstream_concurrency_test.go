package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpstreamConcurrencyClassification(t *testing.T) {
	body := []byte(`{"error":{"code":"TOO_MANY_REQUESTS","type":"billing_error","message":"In-flight request cap reached (1). Retry shortly."}}`)
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 832, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, 429, nil, body))
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, account.Schedulable)
	require.Equal(t, StatusActive, account.Status)
	err := svc.newOpenAIAccountFailoverError(account, 429, nil, body, "", false, true)
	require.True(t, err.IsUpstreamConcurrencyLimited())
	require.True(t, err.ShouldRetryNextAccount())
	require.False(t, err.RetryableOnSameAccount, "try another account before waiting")
	require.True(t, err.ShouldReportAccountScheduleFailure())
	require.Eventually(t, func() bool { return !svc.isOpenAIAccountRuntimeBlocked(account) }, 3*time.Second, 50*time.Millisecond)
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusBadRequest, string(body)},
		{429, `{"error":{"type":"billing_error","message":"Insufficient balance"}}`},
		{429, `{"error":{"message":"quota exceeded"}}`},
		{429, `{"input":"In-flight request cap reached (1). Retry shortly."}`},
	} {
		require.False(t, isUpstreamConcurrencyLimit(tc.status, []byte(tc.body)))
	}
}

func TestUpstreamConcurrencyProviderMessages(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"Concurrency limit exceeded for account, please retry later"}}`,
		`{"error":{"message":"当前账号并发请求过多，请等待已有请求完成"}}`,
		`{"type":"response.failed","response":{"error":{"message":"Concurrency limit exceeded for account, please retry later"}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			account := &Account{ID: 832, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
			require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, 429, nil, []byte(body)))
			require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
			failure := svc.newOpenAIAccountFailoverError(account, 429, nil, []byte(body), "", false, true)
			require.True(t, failure.ShouldRetryNextAccount())
			require.True(t, failure.ShouldReportAccountScheduleFailure())
			require.False(t, failure.RetryableOnSameAccount)
			_, _, healthFailure := classifyOpenAIAPIKeyHealthFailure(failure)
			require.False(t, healthFailure)
			stats := newOpenAIAccountRuntimeStats()
			stats.reportModel(account.ID, "busy-model", false, nil)
			errorRate, _, _, samples := stats.snapshotModel(account.ID, "busy-model")
			require.Equal(t, int64(1), samples)
			require.Equal(t, 1.0, errorRate)
		})
	}
}

func TestUpstreamConcurrencyServiceBusyDoesNotTripHealthBreaker(t *testing.T) {
	body := []byte(`{"error":{"code":"service_busy","message":"模型服务当前并发繁忙，请稍后重试","type":"service_busy"}}`)
	failure := (&OpenAIGatewayService{}).newOpenAIAccountFailoverError(nil, 503, nil, body, "", false, true)
	require.True(t, failure.IsUpstreamConcurrencyLimited())
	require.True(t, failure.ShouldReportAccountScheduleFailure())
	require.False(t, failure.RetryableOnSameAccount)
	_, _, eligible := classifyOpenAIAPIKeyHealthFailure(failure)
	require.False(t, eligible)
	require.False(t, isUpstreamConcurrencyLimit(503, []byte(`{"error":{"message":"Service temporarily unavailable"}}`)))
}
