//go:build unit

package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newAutoModelForwardingTestContext(t *testing.T, path string, body []byte, auto bool) *gin.Context {
	t.Helper()
	ctx := context.Background()
	if auto {
		var err error
		ctx, err = (&SettingService{}).BindAutoModelRoutingPolicy(ctx)
		require.NoError(t, err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func requireAutoModelForwardingBlocked(t *testing.T, c *gin.Context, err error) {
	t.Helper()
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
	require.Equal(t, http.StatusServiceUnavailable, failover.ClientStatusCode)
	require.Equal(t, GatewayFailureScopeRequest, failover.Scope)
	require.Equal(t, GatewayFailureStageInference, failover.Stage)
	require.Equal(t, GatewayFailureReason("auto_model_excluded"), failover.Reason)
	require.True(t, failover.SkipAccountScheduleFailure)
	require.Empty(t, failover.ResponseBody)
	require.False(t, c.Writer.Written())
}

func TestAutoModelForwardBlocksHighestTierAfterResponsesChatFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		auto bool
	}{
		{name: "auto", auto: true},
		{name: "manual"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","input":"hello","stream":false}`)
			c := newAutoModelForwardingTestContext(t, "/v1/responses", body, tc.auto)
			account := rawChatCompletionsTestAccount()
			account.Credentials["model_mapping"] = map[string]any{
				"gpt-5.6-sol": "gpt-5.5",
				"gpt-5.5":     "gpt-6-astra",
			}
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"message":"Upstream request failed","type":"upstream_error","param":"","code":"upstream_error"}}`),
				newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"chatcmpl_cost","object":"chat.completion","model":"gpt-6-astra","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			result, err := svc.Forward(c.Request.Context(), c, account, body)

			if tc.auto {
				require.Nil(t, result)
				requireAutoModelForwardingBlocked(t, c, err)
				require.Len(t, upstream.bodies, 1)
				require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.bodies[0], "model").String())
				require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 2)
			require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.bodies[0], "model").String())
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.bodies[1], "model").String())
			require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
			require.Equal(t, "/v1/chat/completions", upstream.requests[1].URL.Path)
			require.Equal(t, "gpt-6-astra", result.UpstreamModel)
		})
	}
}

func TestAutoModelForwardBlocksGrokModelAfterProviderPrefixNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := NewSettingService(newMockSettingRepo(), nil)
	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{
		Enabled: false,
		Tiers: []ModelCapabilityTier{
			{Name: "highest", Models: []string{"grok-4.5"}},
			{Name: "standard", Models: []string{"grok-4.6"}},
		},
	}))
	autoCtx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.True(t, AutoModelAllowed(autoCtx, "grok/grok-4.5"))
	require.False(t, AutoModelAllowed(autoCtx, "grok-4.5"))

	for _, tc := range []struct {
		name string
		auto bool
	}{
		{name: "auto", auto: true},
		{name: "manual"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"public-grok","input":"hello","stream":false}`)
			c := newAutoModelForwardingTestContext(t, "/v1/responses", body, false)
			if tc.auto {
				c.Request = c.Request.WithContext(autoCtx)
			}
			account := &Account{
				ID: 202, Name: "grok-api-key", Platform: PlatformGrok, Type: AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key":       "xai-test-key",
					"base_url":      "https://api.x.ai/v1",
					"model_mapping": map[string]any{"public-grok": "grok/grok-4.5"},
				},
			}
			upstream := &httpUpstreamRecorder{resp: newOpenAIRejectedFieldTestResponse(http.StatusOK,
				`{"id":"resp_grok_cost","object":"response","status":"completed","model":"grok-4.5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`)}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			result, err := svc.Forward(c.Request.Context(), c, account, body)

			if tc.auto {
				require.Nil(t, result)
				requireAutoModelForwardingBlocked(t, c, err)
				require.Empty(t, upstream.requests)
				require.Empty(t, upstream.bodies)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.bodies[0], "model").String())
			require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
			require.Equal(t, "grok-4.5", result.UpstreamModel)
		})
	}
}

func TestAutoModelForwardRejectsGlobalHighestTierCompactModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name        string
		auto        bool
		passthrough bool
	}{
		{name: "normal_auto", auto: true},
		{name: "normal_manual"},
		{name: "passthrough_auto", auto: true, passthrough: true},
		{name: "passthrough_manual", passthrough: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","stream":false,"instructions":"compact-test","input":[]}`)
			c := newAutoModelForwardingTestContext(t, "/v1/responses/compact", body, tc.auto)
			account := rawChatCompletionsTestAccount()
			account.Extra = map[string]any{
				openai_compat.ExtraKeyResponsesSupported: true,
				"openai_passthrough":                     tc.passthrough,
			}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.OpenAICompactModel = "gpt-6-astra"
			upstream := &httpUpstreamRecorder{resp: newOpenAIRejectedFieldTestResponse(http.StatusOK,
				`{"id":"resp_compact_cost","status":"completed","model":"gpt-6-astra","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

			result, err := svc.Forward(c.Request.Context(), c, account, body)

			if tc.auto {
				require.Nil(t, result)
				requireAutoModelForwardingBlocked(t, c, err)
				require.Empty(t, upstream.requests)
				require.Empty(t, upstream.bodies)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.bodies[0], "model").String())
			require.Equal(t, "/v1/responses/compact", upstream.requests[0].URL.Path)
		})
	}
}

func TestAutoModelNativeCompactRetryFiltersHighestTierTargets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		auto    bool
		account bool
	}{
		{name: "global_auto", auto: true},
		{name: "global_manual"},
		{name: "account_auto", auto: true, account: true},
		{name: "account_manual", account: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
			c := newAutoModelForwardingTestContext(t, "/v1/responses", body, tc.auto)
			MarkOpenAINativeCompactionV2(c)
			account := rawChatCompletionsTestAccount()
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.OpenAICompactModel = "gpt-6-astra"
			if tc.account {
				cfg.Gateway.OpenAICompactModel = "gpt-5.6-sol"
				account.Credentials["compact_model_mapping"] = map[string]any{"gpt-5.5": "gpt-6-astra"}
			}
			svc := &OpenAIGatewayService{cfg: cfg}
			errorBody := []byte(`{"error":{"code":"context_length_exceeded","message":"context window exceeded"}}`)

			retryBody, fallbackModel, retry := svc.prepareOpenAICompactFallbackRetry(
				c, account, "gpt-5.5", body, http.StatusBadRequest, "context window exceeded", errorBody, false,
			)

			if tc.auto {
				require.False(t, retry)
				require.Empty(t, fallbackModel)
				require.Equal(t, body, retryBody)
				return
			}
			require.True(t, retry)
			require.Equal(t, "gpt-6-astra", fallbackModel)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(retryBody, "model").String())
			require.True(t, HasCompactionTriggerInInput(retryBody))
			require.True(t, isOpenAINativeCompactionV2(c))
		})
	}
}

func TestAutoModelForwardSkipsHighestTierNativeCompactRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name        string
		auto        bool
		passthrough bool
	}{
		{name: "normal_auto", auto: true},
		{name: "normal_manual"},
		{name: "passthrough_auto", auto: true, passthrough: true},
		{name: "passthrough_manual", passthrough: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","stream":false,"instructions":"compact-test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
			c := newAutoModelForwardingTestContext(t, "/v1/responses", body, tc.auto)
			MarkOpenAINativeCompactionV2(c)
			account := rawChatCompletionsTestAccount()
			account.Extra = map[string]any{
				openai_compat.ExtraKeyResponsesSupported: true,
				"openai_passthrough":                     tc.passthrough,
			}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.OpenAICompactModel = "gpt-6-astra"
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"context_length_exceeded","message":"context window exceeded"}}`),
				newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_compact_retry_cost","status":"completed","model":"gpt-6-astra","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
			}}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

			result, err := svc.Forward(c.Request.Context(), c, account, body)

			if tc.auto {
				require.Error(t, err)
				require.Nil(t, result)
				require.Len(t, upstream.bodies, 1)
				require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.bodies[0], "model").String())
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 2)
			require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.bodies[0], "model").String())
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.bodies[1], "model").String())
			require.True(t, HasCompactionTriggerInInput(upstream.bodies[1]))
			require.Equal(t, upstream.requests[0].URL.Path, upstream.requests[1].URL.Path)
		})
	}
}
