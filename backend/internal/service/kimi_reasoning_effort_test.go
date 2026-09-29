package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func nvidiaKimiTestAccount() *Account {
	return &Account{
		ID: 180, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "fixture", "base_url": "https://integrate.api.nvidia.com/v1",
			"model_mapping": map[string]any{"public-kimi": "moonshotai/kimi-k3"}},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
	}
}

func TestNormalizeNVIDIAKimiK3ReasoningEffort(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"minimal", "low"}, {"low", "low"}, {"medium", "high"}, {"high", "high"},
		{"xhigh", "max"}, {"x-high", "max"}, {"max", "max"}, {" HIGH ", "high"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"reasoning_effort": tc.input, "max_tokens": 83, "messages": []map[string]string{{"role": "user", "content": "OK"}}})
			require.NoError(t, err)
			got, effort, err := normalizeNVIDIAKimiK3ReasoningEffort(context.Background(), nil, nvidiaKimiTestAccount(), "moonshotai/kimi-k3", body)
			require.NoError(t, err)
			require.NotNil(t, effort)
			require.Equal(t, tc.want, *effort)
			require.Equal(t, tc.want, gjson.GetBytes(got, "reasoning_effort").String())
			require.Equal(t, int64(83), gjson.GetBytes(got, "max_tokens").Int())
			require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(got, "messages").Raw)
		})
	}
	for _, body := range []string{`{"reasoning_effort":"none","thinking":{"type":"disabled"}}`, `{"reasoning_effort":"future"}`, `{"reasoning_effort":false}`, `{"messages":[]}`} {
		got, effort, err := normalizeNVIDIAKimiK3ReasoningEffort(context.Background(), nil, nvidiaKimiTestAccount(), "kimi-k3", []byte(body))
		require.NoError(t, err)
		require.Nil(t, effort)
		require.Equal(t, body, string(got))
	}
}

func TestNVIDIAKimiReasoningEffortDoesNotAffectOtherModelsOrEndpoints(t *testing.T) {
	body := []byte(`{"reasoning_effort":"medium"}`)
	for _, tc := range []struct{ platform, endpoint, model string }{
		{PlatformKimi, "https://integrate.api.nvidia.com/v1", "moonshotai/kimi-k3"},
		{PlatformOpenAI, "https://api.moonshot.ai/v1", "moonshotai/kimi-k3"},
		{PlatformOpenAI, "https://integrate.api.nvidia.com.example/v1", "moonshotai/kimi-k3"},
		{PlatformOpenAI, "https://proxy.example/v1", "moonshotai/kimi-k3"},
		{PlatformOpenAI, "https://integrate.api.nvidia.com/v1", "kimi-k2.5"},
		{PlatformOpenAI, "https://integrate.api.nvidia.com/v1", "kimi-k30"},
		{PlatformOpenAI, "https://integrate.api.nvidia.com/v1", "vendor/kimi-k3"},
	} {
		account := nvidiaKimiTestAccount()
		account.Platform = tc.platform
		account.Credentials["base_url"] = tc.endpoint
		got, effort, err := normalizeNVIDIAKimiK3ReasoningEffort(context.Background(), nil, account, tc.model, body)
		require.NoError(t, err)
		require.Nil(t, effort)
		require.Equal(t, body, got)
	}
}

func TestNVIDIAKimiK3ForwardingNormalizesMappedModelAndUsageEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ path, body, tokenField string }{
		{"/v1/responses", `{"model":"public-kimi","input":"OK","reasoning":{"effort":"medium"},"max_output_tokens":256,"stream":false}`, "max_completion_tokens"},
		{"/v1/chat/completions", `{"model":"public-kimi","messages":[{"role":"user","content":"OK"}],"reasoning_effort":"medium","max_tokens":256,"stream":false}`, "max_tokens"},
		{"/v1/messages", `{"model":"public-kimi","messages":[{"role":"user","content":"OK"}],"output_config":{"effort":"medium"},"max_tokens":256,"stream":false}`, "max_completion_tokens"},
	} {
		for _, policy := range []string{"uncapped", "context_cap", "group_cap", "auto_group_cap"} {
			t.Run(tc.path+"/"+policy, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				body := []byte(tc.body)
				c.Request = httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_kimi","object":"chat.completion","model":"moonshotai/kimi-k3","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)),
				}}
				svc := &OpenAIGatewayService{httpUpstream: upstream, cfg: &config.Config{}}
				account := nvidiaKimiTestAccount()
				ctx := context.Background()
				if policy == "context_cap" {
					ctx = WithOpenAIReasoningEffortPolicy(ctx, "medium", nil, ReasoningEffortOverLimitDowngrade)
				}
				if policy == "group_cap" || policy == "auto_group_cap" {
					c.Set("api_key", &APIKey{Group: &Group{Platform: PlatformOpenAI, MaxReasoningEffort: "medium"}})
				}
				if policy == "auto_group_cap" {
					var bindErr error
					ctx, bindErr = (&SettingService{}).BindAutoModelRoutingPolicy(ctx)
					require.NoError(t, bindErr)
				}
				var result *OpenAIForwardResult
				var err error
				switch tc.path {
				case "/v1/responses":
					result, err = svc.Forward(ctx, c, account, body)
				case "/v1/chat/completions":
					result, err = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
				case "/v1/messages":
					result, err = svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
				}
				if policy != "uncapped" {
					require.Error(t, err)
					require.Nil(t, upstream.lastReq, "不能向上游发送超过分组上限的原生档位")
					if policy == "auto_group_cap" {
						var failover *UpstreamFailoverError
						require.ErrorAs(t, err, &failover)
						require.Equal(t, AutoModelCapabilityMismatchReason, failover.Reason)
						require.True(t, failover.SkipAccountScheduleFailure)
						require.True(t, failover.ShouldRetryNextAccount())
						require.False(t, c.Writer.Written())
						return
					}
					require.True(t, IsReasoningEffortPolicyDenied(err))
					require.Equal(t, http.StatusForbidden, recorder.Code)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "moonshotai/kimi-k3", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
				require.Equal(t, int64(256), gjson.GetBytes(upstream.lastBody, tc.tokenField).Int())
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "high", *result.ReasoningEffort)
			})
		}
	}
}

func TestNVIDIAKimiNativeEffortCannotExceedGroupCeiling(t *testing.T) {
	for _, tc := range []struct{ effort, ceiling string }{{"minimal", "minimal"}, {"medium", "medium"}, {"xhigh", "xhigh"}} {
		ctx := WithOpenAIReasoningEffortPolicy(context.Background(), tc.ceiling, nil, ReasoningEffortOverLimitDowngrade)
		body, err := json.Marshal(map[string]string{"reasoning_effort": tc.effort})
		require.NoError(t, err)
		got, effective, err := normalizeNVIDIAKimiK3ReasoningEffort(ctx, nil, nvidiaKimiTestAccount(), "kimi-k3", body)
		require.True(t, IsReasoningEffortPolicyDenied(err))
		require.Equal(t, body, got)
		require.Nil(t, effective)
	}
	ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "medium", nil, ReasoningEffortOverLimitDeny)
	body := []byte(`{"reasoning_effort":"low"}`)
	got, effective, err := normalizeNVIDIAKimiK3ReasoningEffort(ctx, nil, nvidiaKimiTestAccount(), "kimi-k3", body)
	require.NoError(t, err)
	require.Equal(t, body, got)
	require.Equal(t, "low", *effective)
}
