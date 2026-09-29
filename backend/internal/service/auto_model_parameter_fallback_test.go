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

func TestAutoModelKimiEffortMismatchFallsBackWithoutToolQuarantine(t *testing.T) {
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	repo := &upstreamModelRefreshRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	body := []byte(`{"code":400,"message":"Unsupported Kimi K3 thinking_effort=\"medium\"; supported values are low, high, and max","type":"Bad Request"}`)
	result := svc.newAutoModelCapabilityMismatchFailoverError(ctx, &Account{ID: 180, Platform: PlatformOpenAI}, "moonshotai/kimi-k3", 400, nil, body, "")
	require.NotNil(t, result)
	require.True(t, result.ShouldRetryNextAccount())
	require.Empty(t, repo.updates)
	for _, invalid := range []string{
		`{"message":"Unsupported Kimi K3 thinking_effort=\"banana\"; supported values are low, high, and max"}`,
		`{"input":{"message":"Unsupported Kimi K3 thinking_effort=\"medium\"; supported values are low, high, and max"}}`,
	} {
		require.False(t, isOpenAIParameterCapabilityError(400, []byte(invalid)))
	}
	require.False(t, isOpenAIParameterCapabilityError(401, body))
	require.Nil(t, svc.newAutoModelCapabilityMismatchFailoverError(context.Background(), nil, "kimi-k3", 400, nil, body, ""))
}

func TestAutoModelSearchMismatchDoesNotQuarantineFunctionTools(t *testing.T) {
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	repo := &upstreamModelRefreshRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 42, Platform: PlatformOpenAI}
	for _, body := range []string{
		`{"error":{"message":"Tool type web_search is not supported"}}`,
		`{"response":{"error":{"message":"Unsupported parameter tools: web_search_preview"}}}`,
	} {
		result := svc.newAutoModelCapabilityMismatchFailoverError(ctx, account, "candidate", http.StatusBadRequest, nil, []byte(body), "")
		require.NotNil(t, result)
		require.True(t, result.ShouldRetryNextAccount())
		require.Empty(t, repo.updates, "托管搜索错误不能封禁函数调用能力")
	}
	for _, body := range []string{
		`{"error":{"message":"Invalid web_search configuration"}}`,
		`{"input":{"message":"web_search is not supported"}}`,
	} {
		require.Nil(t, svc.newAutoModelCapabilityMismatchFailoverError(ctx, account, "candidate", http.StatusBadRequest, nil, []byte(body), ""))
	}
	require.False(t, isOpenAISearchToolCapabilityError(http.StatusUnauthorized, "", []byte(`{"message":"web_search is not supported"}`)))
}
