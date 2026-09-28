//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDeepseekWebAccountUsesOnlyBuiltInAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, DeepseekWebURL: "http://deepseek-adapter:7867", DeepseekWebKey: "shared-key",
	})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	credentials := map[string]any{"api_protocol": APIProtocolChatCompletions}
	applyBuiltinAdapterCredentials(PlatformDeepseekWeb, credentials)
	require.NoError(t, validateBuiltinChatCredentials(PlatformDeepseekWeb, AccountTypeAPIKey, credentials))
	require.Equal(t, "http://deepseek-adapter:7867/v1", credentials["base_url"])
	require.Equal(t, "shared-key", credentials["api_key"])

	account := &Account{Platform: PlatformDeepseekWeb, Type: AccountTypeAPIKey, Credentials: credentials}
	require.Equal(t, "http://deepseek-adapter:7867/v1", account.GetOpenAIBaseURL())
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.False(t, account.SupportsNativeCNResponses())
	require.Empty(t, account.GetAccountMode())
	require.Equal(t, []string{"deepseek-web-chat", "deepseek-web-reasoner"}, DefaultDeepseekWebModelIDs())
	require.Error(t, validateBuiltinChatCredentials(PlatformDeepseekWeb, AccountTypeOAuth, credentials))
	credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateBuiltinChatCredentials(PlatformDeepseekWeb, AccountTypeAPIKey, credentials))
}

func TestDeepseekWebBrowserLoginProxyDoesNotExposeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer shared-key", r.Header.Get("Authorization"))
		require.Equal(t, "admin:42", r.Header.Get("X-Login-Owner"))
		require.Equal(t, "/internal/login/sessions", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": strings.Repeat("a", 64),
			"auth_url": "https://chat.deepseek.com/sign_in",
			"mode": "callback", "status": "pending", "expires_at": 1,
		})
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, DeepseekWebURL: server.URL, DeepseekWebKey: "shared-key",
	})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	result, err := BuiltinAdapterLogin(context.Background(), PlatformDeepseekWeb, "admin:42", "", "start", "")
	require.NoError(t, err)
	require.Equal(t, "https://chat.deepseek.com/sign_in", result.AuthURL)
	require.Equal(t, "callback", result.Mode)
	require.NotContains(t, result.AuthURL, "shared-key")
}
