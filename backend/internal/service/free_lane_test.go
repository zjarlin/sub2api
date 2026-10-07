//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyFreeLaneRequestHeaders_OpenCodeFree(t *testing.T) {
	account := &Account{Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey, Credentials: map[string]any{"account_mode": AccountModeFree}}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer sk-user")

	applyFreeLaneRequestHeaders(account, headers, "conversation-1")

	require.Equal(t, "Bearer public", headers.Get("Authorization"))
	require.Equal(t, openCodeClientName, headers.Get(openCodeClientHeader))
	require.Equal(t, openCodeProjectName, headers.Get(openCodeProjectHeader))
	require.NotEmpty(t, headers.Get(openCodeSessionHeader))
	require.Equal(t, openCodeFreeUserAgent, headers.Get("User-Agent"))
}

func TestApplyFreeLaneRequestHeaders_KiloStripsAuth(t *testing.T) {
	account := &Account{Platform: PlatformKilo, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "public"}}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer public")
	headers.Set("x-api-key", "public")

	applyFreeLaneRequestHeaders(account, headers, "conversation-1")

	require.Empty(t, headers.Get("Authorization"))
	require.Empty(t, headers.Get("x-api-key"))
	require.Equal(t, kiloUserAgent, headers.Get("User-Agent"))
}

func TestApplyFreeLaneRequestHeaders_LeavesPaidAccountsUntouched(t *testing.T) {
	account := &Account{Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey, Credentials: map[string]any{"account_mode": AccountModeZen}}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer sk-zen")

	applyFreeLaneRequestHeaders(account, headers, "conversation-1")

	require.Equal(t, "Bearer sk-zen", headers.Get("Authorization"))
	require.Empty(t, headers.Get(openCodeClientHeader))
}

func TestApplyFreeLaneBodyFingerprint_AddsQuartet(t *testing.T) {
	account := &Account{Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey, Credentials: map[string]any{"account_mode": AccountModeFree}}
	body := []byte(`{"model":"glm-5.3","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"bash","description":"b","parameters":{"type":"object"}}}]}`)

	out := applyFreeLaneBodyFingerprint(account, body, freeLaneToolStyleChat)

	names := map[string]bool{}
	for _, tool := range gjson.GetBytes(out, "tools").Array() {
		names[tool.Get("function.name").String()] = true
	}
	for _, name := range openCodeFreeFingerprintTools {
		require.True(t, names[name], "missing fingerprint tool %q", name)
	}
	require.Equal(t, "auto", gjson.GetBytes(out, "tool_choice").String())
}

func TestApplyFreeLaneBodyFingerprint_AnthropicStyle(t *testing.T) {
	account := &Account{Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey, Credentials: map[string]any{"account_mode": AccountModeFree}}
	body := []byte(`{"model":"union-alpha","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)

	out := applyFreeLaneBodyFingerprint(account, body, freeLaneToolStyleAnthropic)

	first := gjson.GetBytes(out, "tools.0")
	require.True(t, first.Get("input_schema").Exists())
	require.False(t, first.Get("function").Exists())
}

func TestApplyFreeLaneBodyFingerprint_KiloUntouched(t *testing.T) {
	account := &Account{Platform: PlatformKilo, Type: AccountTypeAPIKey}
	body := []byte(`{"model":"kilo-auto/free","messages":[]}`)

	require.Equal(t, body, applyFreeLaneBodyFingerprint(account, body, freeLaneToolStyleChat))
}

func TestSendCCUpstreamRequest_OpenCodeFreeInjectsPublicAuthAndFingerprint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &openCodeSessionHTTPUpstream{}
	svc := openCodeSessionTestService()
	svc.httpUpstream = upstream
	account := &Account{
		ID: 9, Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"account_mode": AccountModeFree},
	}
	c := newOpenCodeSessionTestContext(t, "conversation-free")

	resp, err := svc.sendCCUpstreamRequest(
		context.Background(), c, account,
		"https://opencode.ai/zen/v1/chat/completions", []byte(`{"model":"glm-5.3"}`),
		false, "public", "", "",
	)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "Bearer public", upstream.request.Header.Get("Authorization"))
	require.Equal(t, openCodeClientName, upstream.request.Header.Get(openCodeClientHeader))
	require.Equal(t, openCodeFreeUserAgent, upstream.request.Header.Get("User-Agent"))
}

func TestSendCCUpstreamRequest_KiloStripsAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &openCodeSessionHTTPUpstream{}
	svc := openCodeSessionTestService()
	svc.httpUpstream = upstream
	account := &Account{ID: 10, Platform: PlatformKilo, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "public"}}
	c := newOpenCodeSessionTestContext(t, "conversation-kilo")

	resp, err := svc.sendCCUpstreamRequest(
		context.Background(), c, account,
		"https://api.kilo.ai/api/gateway/chat/completions", []byte(`{"model":"kilo-auto/free"}`),
		false, "public", "", "",
	)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Empty(t, upstream.request.Header.Get("Authorization"))
	require.Equal(t, kiloUserAgent, upstream.request.Header.Get("User-Agent"))
}
