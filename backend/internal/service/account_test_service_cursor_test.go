//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func cursorConnectionTestAccount(t *testing.T) *Account {
	t.Helper()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, CursorURL: "http://cursor.internal:7868", CursorKey: "internal-cursor-key",
	})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	return &Account{
		ID: 971, Platform: PlatformCursor, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "cursor-dashboard-key", "api_protocol": APIProtocolChatCompletions},
	}
}

func TestAccountTestService_CursorDefaultModelUsesAccountCatalog(t *testing.T) {
	for _, tc := range []struct {
		name    string
		models  []string
		mapping map[string]any
		want    string
	}{
		{name: "synced", models: []string{"z-model", "a-model"}, want: "a-model"},
		{name: "whitelist", models: []string{"a-model", "z-model"}, mapping: map[string]any{"public-model": "z-model"}, want: "z-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := cursorConnectionTestAccount(t)
			account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{Source: "upstream", Models: tc.models})
			if tc.mapping != nil {
				account.Credentials["model_mapping"] = tc.mapping
			}
			svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
			c, recorder := newTestContext()

			err := svc.TestAccountConnection(c, account.ID, "", "hello", AccountTestModeDefault)

			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "http://cursor.internal:7868/v1/chat/completions", upstream.lastReq.URL.String())
			require.Equal(t, "Bearer cursor-dashboard-key", upstream.lastReq.Header.Get("Authorization"))
			require.Equal(t, "internal-cursor-key", upstream.lastReq.Header.Get("X-Sub2API-Adapter-Key"))
			require.Equal(t, tc.want, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Contains(t, recorder.Body.String(), `"success":true`)
		})
	}
}

func TestAccountTestService_CursorWithoutCatalogDiscoversModelBeforeGeneration(t *testing.T) {
	account := cursorConnectionTestAccount(t)
	svc, upstream := adaptiveCNAccountTestService(account,
		newJSONResponse(http.StatusOK, `{"data":[{"id":"actual-cursor-model"}]}`), adaptiveCNChatTestResponse())
	c, _ := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "", "hello", AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://cursor.internal:7868/v1/models", upstream.requests[0].URL.String())
	require.Equal(t, "actual-cursor-model", gjson.GetBytes(upstream.lastBody, "model").String())
	for _, req := range upstream.requests {
		require.Equal(t, "Bearer cursor-dashboard-key", req.Header.Get("Authorization"))
		require.Equal(t, "internal-cursor-key", req.Header.Get("X-Sub2API-Adapter-Key"))
	}
}

func TestAccountTestService_CursorCatalogFailureDoesNotGuessModel(t *testing.T) {
	account := cursorConnectionTestAccount(t)
	svc, upstream := adaptiveCNAccountTestService(account, newJSONResponse(http.StatusUnauthorized, `{"error":{"code":"cursor_authentication_failed"}}`))
	c, recorder := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "", "hello", AccountTestModeDefault)

	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/models", upstream.lastReq.URL.Path)
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_CursorCustomAdapterDoesNotReceiveInternalKey(t *testing.T) {
	account := cursorConnectionTestAccount(t)
	account.Credentials["base_url"] = "http://custom-cursor.example/v1"
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, _ := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "actual-model", "hello", AccountTestModeDefault)

	require.NoError(t, err)
	require.Equal(t, "http://custom-cursor.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer cursor-dashboard-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Sub2API-Adapter-Key"))
}

func TestAccountTestService_CursorHealthProbeUsesSupportedTextParameters(t *testing.T) {
	account := cursorConnectionTestAccount(t)
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())

	result, err := svc.RunTestBackground(context.Background(), account.ID, "actual-model", AccountTestOptions{HealthProbe: true})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, accountHealthProbePrompt, gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	for _, field := range []string{"max_tokens", "max_completion_tokens", "temperature", "tools"} {
		require.False(t, gjson.GetBytes(upstream.lastBody, field).Exists(), field)
	}
}
