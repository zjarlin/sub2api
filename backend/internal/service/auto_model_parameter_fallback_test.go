package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoModelUnsupportedParameterFallsThroughWithoutToolQuarantine(t *testing.T) {
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	repo := &upstreamModelRefreshRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 42, Platform: PlatformVibex}
	body := []byte(`{"error":{"code":"unsupported_parameter","message":"VibeX does not support parameter: max_completion_tokens","type":"upstream_error"}}`)
	result := svc.newAutoModelCapabilityMismatchFailoverError(ctx, account, "candidate", http.StatusBadRequest, http.Header{}, body, "")
	require.NotNil(t, result)
	require.Equal(t, AutoModelCapabilityMismatchReason, result.Reason)
	require.Equal(t, NextAccountRetry, result.NextAccountAction)
	require.Equal(t, body, result.ResponseBody)
	require.Empty(t, repo.updates, "输出预算不支持不能隔离该模型的工具能力")
	require.Nil(t, svc.newAutoModelCapabilityMismatchFailoverError(context.Background(), account, "candidate", http.StatusBadRequest, nil, body, ""))
}

func TestAutoModelParameterMismatchDoesNotRetryOtherClientErrors(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"invalid_value","message":"Invalid max_output_tokens"}}`,
		`{"error":{"code":"invalid_request_error","message":"input is required"}}`,
		`{"error":{"code":"context_length_exceeded"}}`,
		`{"error":{"code":"cyber_policy"}}`,
		`{"error":{"message":"bad request"},"input":{"error":{"code":"unsupported_parameter"}}}`,
		`unsupported_parameter`,
	} {
		require.False(t, isOpenAIParameterCapabilityError(http.StatusBadRequest, []byte(body)), body)
	}
	require.False(t, isOpenAIParameterCapabilityError(http.StatusUnauthorized, []byte(`{"error":{"code":"unsupported_parameter"}}`)))
	require.True(t, isOpenAIParameterCapabilityError(http.StatusUnprocessableEntity, []byte(`{"response":{"error":{"code":"unsupported_parameter"}}}`)))
}
