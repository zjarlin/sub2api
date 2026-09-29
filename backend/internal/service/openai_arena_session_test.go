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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func arenaTestContext(keyID int64) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("api_key", &APIKey{ID: keyID})
	return c
}

func arenaTestAccount() *Account {
	account := forceChatResponsesFallbackAccount()
	account.Extra["openai_session_adapter"] = "arena"
	return account
}

func TestArenaPlatformAccountConfigurationAndProtocol(t *testing.T) {
	input := &CreateAccountInput{
		Name: "Arena", Platform: PlatformArena, Type: AccountTypeAPIKey, Concurrency: 32,
		Credentials: map[string]any{"base_url": "http://sub2api-arena:7867/v1", "api_key": "adapter-key"},
	}
	account, err := buildAccountForCreate(input, map[string]any{"custom": true, "model_health_probe_enabled": true})
	require.NoError(t, err)
	require.True(t, account.IsOpenAICompatible())
	require.True(t, isArenaSessionAdapter(account))
	require.Equal(t, 1, account.Concurrency)
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.Equal(t, "http://sub2api-arena:7867/v1", account.GetOpenAIBaseURL())
	require.Equal(t, "force_chat_completions", account.GetExtraString("openai_responses_mode"))
	require.Equal(t, true, account.Extra["custom"])
	require.False(t, account.ModelProbePolicy().Enabled)
	account.Extra = map[string]any{"model_health_probe_enabled": true}
	require.False(t, account.ModelProbePolicy().Enabled)
	require.True(t, IsAllowedQuotaPlatform(PlatformArena))
	require.Empty(t, defaultModelsListCandidateIDs(PlatformArena))
	platform, detected := DetectModelPlatform("arena-session")
	require.True(t, detected)
	require.Equal(t, PlatformArena, platform)
	for _, credentials := range []map[string]any{
		{"api_key": "adapter-key"},
		{"base_url": "http://sub2api-arena:7867/v1"},
		{"base_url": "file:///local", "api_key": "adapter-key"},
		{"base_url": "http://sub2api-arena:7867/v1", "api_key": "adapter-key", "api_protocol": "responses"},
	} {
		require.Error(t, validateArenaCredentials(AccountTypeAPIKey, credentials))
	}
	require.Error(t, validateArenaCredentials(AccountTypeOAuth, input.Credentials))
}

func TestArenaSessionHeadersIsolateTenantsAndRequests(t *testing.T) {
	first := arenaTestContext(41)
	second := arenaTestContext(42)
	for _, c := range []*gin.Context{first, second} {
		c.Request.Header.Set("X-Codex-Session-Id", "same-client-session")
		c.Request.Header.Set("Idempotency-Key", "turn-1")
	}
	firstHeaders, secondHeaders := http.Header{}, http.Header{}
	applyArenaSessionHeaders(first, arenaTestAccount(), firstHeaders, nil)
	applyArenaSessionHeaders(second, arenaTestAccount(), secondHeaders, nil)
	require.NotEmpty(t, firstHeaders.Get("X-Arena-Session-Id"))
	require.NotEqual(t, firstHeaders.Get("X-Arena-Session-Id"), secondHeaders.Get("X-Arena-Session-Id"))
	require.NotEqual(t, firstHeaders.Get("X-Arena-Idempotency-Key"), secondHeaders.Get("X-Arena-Idempotency-Key"))
	repeated := http.Header{}
	applyArenaSessionHeaders(first, arenaTestAccount(), repeated, nil)
	require.Equal(t, firstHeaders, repeated)
	first.Request.Header.Set("Idempotency-Key", "turn-2")
	applyArenaSessionHeaders(first, arenaTestAccount(), repeated, nil)
	require.Equal(t, firstHeaders.Get("X-Arena-Session-Id"), repeated.Get("X-Arena-Session-Id"))
	require.NotEqual(t, firstHeaders.Get("X-Arena-Idempotency-Key"), repeated.Get("X-Arena-Idempotency-Key"))
}

func TestArenaSessionHeadersRecoverResponsesIdentityAndRejectStaticOverrides(t *testing.T) {
	c := arenaTestContext(41)
	rememberOpenCodeInboundBody(c, []byte(`{"prompt_cache_key":"responses-session"}`))
	headers := http.Header{"X-Arena-Session-Id": {"static"}, "X-Arena-Workspace": {"/private"}, "X-Codex-Session-Id": {"static"}, "Idempotency-Key": {"static"}}
	applyArenaSessionHeaders(c, arenaTestAccount(), headers, []byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	require.Equal(t, arenaNamespacedIdentity(41, "responses-session"), headers.Get("X-Arena-Session-Id"))
	require.Empty(t, headers.Get("X-Arena-Workspace"))
	require.Empty(t, headers.Get("X-Codex-Session-Id"))
	require.Empty(t, headers.Get("Idempotency-Key"))
	applyArenaSessionHeaders(arenaTestContext(0), arenaTestAccount(), headers, nil)
	require.Empty(t, headers.Get("X-Arena-Session-Id"))
	ordinaryHeaders := http.Header{}
	applyArenaSessionHeaders(c, forceChatResponsesFallbackAccount(), ordinaryHeaders, nil)
	require.Empty(t, ordinaryHeaders)
}

func TestArenaCCPipelinePropagatesCancellationAndIdentity(t *testing.T) {
	c := arenaTestContext(41)
	c.Request.Header.Set("X-Codex-Session-Id", "conversation")
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := svc.sendCCUpstreamRequest(ctx, c, arenaTestAccount(), "http://upstream.example/v1/chat/completions", []byte(`{"model":"arena-session","messages":[{"role":"user","content":"hello"}]}`), false, "adapter-key", "", "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, arenaNamespacedIdentity(41, "conversation"), upstream.lastReq.Header.Get("X-Arena-Session-Id"))
	cancel()
	require.ErrorIs(t, upstream.lastReq.Context().Err(), context.Canceled)
}

func TestArenaResponsesConversionPreservesSessionAndSystem(t *testing.T) {
	body := []byte(`{"model":"arena-session","prompt_cache_key":"responses-session","instructions":"Answer in Chinese","input":"hello","stream":false}`)
	c := arenaTestContext(41)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_arena","object":"chat.completion","model":"arena-session","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := arenaTestAccount()
	account.Platform = PlatformArena
	account.Extra = nil
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, arenaNamespacedIdentity(41, "responses-session"), upstream.lastReq.Header.Get("X-Arena-Session-Id"))
	require.Contains(t, string(upstream.lastBody), "Answer in Chinese")
}

func TestArenaCCStreamErrorDoesNotBecomeSuccessfulResponse(t *testing.T) {
	c := arenaTestContext(41)
	c.Set(arenaSessionAdapterContextKey, true)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"error\":{\"message\":\"Arena failed\"}}\n\ndata: [DONE]\n\n"))}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	state := svc.scanCCStream(c, resp, "arena test", "request", time.Now(), nil)
	require.ErrorContains(t, state.Err, "Arena failed")
	require.False(t, state.SawDone)
}

func TestArenaResponsesStreamFailureDoesNotEmitCompleted(t *testing.T) {
	body := []byte(`{"model":"arena-session","prompt_cache_key":"responses-session","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: 41})
	stream := "data: {\"id\":\"chatcmpl_arena\",\"model\":\"arena-session\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\ndata: {\"error\":{\"message\":\"Arena failed\"}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	result, err := svc.Forward(context.Background(), c, arenaTestAccount(), body)
	require.ErrorContains(t, err, "Arena failed")
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.NotContains(t, rec.Body.String(), "event: response.completed")
	require.NotContains(t, rec.Body.String(), "data: [DONE]")
}
