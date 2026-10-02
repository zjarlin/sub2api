//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func windsurfConnectionTestAccount(t *testing.T) *Account {
	t.Helper()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, WindsurfURL: "http://windsurf.internal:7869", WindsurfKey: "internal-windsurf-key",
	})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	return &Account{
		ID: 972, Platform: PlatformWindsurf, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "devin-session-token$test", "api_protocol": APIProtocolChatCompletions},
	}
}

func TestAccountTestService_WindsurfDefaultModelUsesAccountCatalog(t *testing.T) {
	account := windsurfConnectionTestAccount(t)
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{Source: "upstream", Models: []string{"z-model", "a-model"}})
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorder := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "", "hello", AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://windsurf.internal:7869/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer devin-session-token$test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "internal-windsurf-key", upstream.lastReq.Header.Get("X-Sub2API-Adapter-Key"))
	require.Equal(t, "a-model", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_WindsurfWithoutCatalogDiscoversModelBeforeGeneration(t *testing.T) {
	account := windsurfConnectionTestAccount(t)
	svc, upstream := adaptiveCNAccountTestService(account,
		newJSONResponse(http.StatusOK, `{"data":[{"id":"claude-opus-4-8-medium"}]}`), adaptiveCNChatTestResponse())
	c, _ := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "", "hello", AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://windsurf.internal:7869/v1/models", upstream.requests[0].URL.String())
	require.Equal(t, "claude-opus-4-8-medium", gjson.GetBytes(upstream.lastBody, "model").String())
	for _, req := range upstream.requests {
		require.Equal(t, "Bearer devin-session-token$test", req.Header.Get("Authorization"))
		require.Equal(t, "internal-windsurf-key", req.Header.Get("X-Sub2API-Adapter-Key"))
	}
}

func TestWindsurfPlatformWiring(t *testing.T) {
	require.True(t, IsMultiProtocolAPIKeyProvider(PlatformWindsurf))
	require.True(t, IsAllowedQuotaPlatform(PlatformWindsurf))
	require.Contains(t, AllowedQuotaPlatforms, PlatformWindsurf)
	require.Equal(t, []string{PlatformOpenAI}, MixedSchedulingTargetPlatforms(PlatformWindsurf))
	require.Contains(t, MixedSchedulingSourcePlatforms(PlatformOpenAI), PlatformWindsurf)
	require.Equal(t, PlatformWindsurf, NormalizeOpenAICompatiblePlatform(PlatformWindsurf))
	account := windsurfConnectionTestAccount(t)
	require.True(t, account.IsOpenAICompatible())
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.NoError(t, validateBuiltinChatCredentials(PlatformWindsurf, AccountTypeAPIKey, map[string]any{
		"api_key":      "devin-session-token$test",
		"api_protocol": APIProtocolChatCompletions,
	}))
	require.Error(t, validateBuiltinChatCredentials(PlatformWindsurf, AccountTypeOAuth, map[string]any{"api_key": "x"}))
}
