//go:build unit

package service

import (
	"bytes"
	"context"
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

func TestVibexAccountIntegration(t *testing.T) {
	previous := builtinAdapterConfig.Load()
	t.Cleanup(func() { builtinAdapterConfig.Store(previous) })
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, VibexKey: "fixture-key"})
	credentials := map[string]any{"api_protocol": APIProtocolChatCompletions}
	applyBuiltinAdapterCredentials(PlatformVibex, credentials)
	require.Equal(t, "http://sub2api-vibex:7866/v1", credentials["base_url"])
	require.Equal(t, "fixture-key", credentials["api_key"])
	require.NoError(t, validateBuiltinChatCredentials(PlatformVibex, AccountTypeAPIKey, credentials))
	require.Error(t, validateBuiltinChatCredentials(PlatformVibex, AccountTypeOAuth, credentials))
	credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateBuiltinChatCredentials(PlatformVibex, AccountTypeAPIKey, credentials))
	a := &Account{Platform: PlatformVibex, Type: AccountTypeAPIKey, Credentials: credentials}
	require.Equal(t, APIProtocolChatCompletions, a.GetAPIProtocol())
	require.True(t, IsCNProvider(PlatformVibex))
	require.True(t, IsAllowedQuotaPlatform(PlatformVibex))
	require.Equal(t, 1, normalizeAccountConcurrency(PlatformVibex, AccountTypeAPIKey, 8))
	require.True(t, mixedSchedulingTargetsPlatform(PlatformVibex, PlatformOpenAI))
	require.Empty(t, defaultModelsListCandidateIDs(PlatformVibex))
}

func TestVibexChatOmitsUnsupportedReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"vibex-alias","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl-vibex","object":"chat.completion","model":"free-qwen-3.8-max","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, vibexBridgeTestAccount(), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ReasoningEffort)
	require.Equal(t, "free-qwen-3.8-max", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning_effort").Exists())
	require.Equal(t, "ok", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
}

func TestVibexChatOmitsUnsupportedCompletionBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"vibex-alias","messages":[{"role":"user","content":"hello"}],"max_tokens":64,"max_completion_tokens":64}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl-vibex","object":"chat.completion","model":"free-qwen-3.8-max","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, vibexBridgeTestAccount(), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Exists())
}

func TestVibexResponsesOmitsUnsupportedReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"vibex-alias","input":"hello","reasoning":{"effort":"high"}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl-vibex","object":"chat.completion","model":"free-qwen-3.8-max","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, vibexBridgeTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ReasoningEffort)
	require.Equal(t, "free-qwen-3.8-max", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning_effort").Exists())
	require.Equal(t, "ok", gjson.Get(recorder.Body.String(), "output.0.content.0.text").String())
}

func TestVibexAnthropicFallbackOmitsUnsupportedMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"vibex-alias","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl-vibex","object":"chat.completion","model":"free-qwen-3.8-max","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		)),
	}}
	account := vibexBridgeTestAccount()
	account.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
	}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Exists())
	require.Equal(t, "ok", gjson.Get(recorder.Body.String(), "content.0.text").String())
}
