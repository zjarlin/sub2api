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
	require.False(t, err.ShouldReportAccountScheduleFailure())
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
