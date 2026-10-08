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

func TestMadaoAccountUsesOnlyBuiltInAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, MadaoURL: "http://madao-adapter:7870", MadaoKey: "shared-key",
	})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	credentials := map[string]any{"api_protocol": APIProtocolChatCompletions}
	applyBuiltinAdapterCredentials(PlatformMadao, credentials)
	require.NoError(t, validateBuiltinChatCredentials(PlatformMadao, AccountTypeAPIKey, credentials))
	require.Equal(t, "http://madao-adapter:7870/v1", credentials["base_url"])
	require.Equal(t, "shared-key", credentials["api_key"])

	account := &Account{Platform: PlatformMadao, Type: AccountTypeAPIKey, Credentials: credentials}
	require.Equal(t, "http://madao-adapter:7870/v1", account.GetOpenAIBaseURL())
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.False(t, account.SupportsNativeCNResponses())
	require.Empty(t, account.GetAccountMode())
	require.Equal(t, []string{"GLM-5.2", "GLM-5.1", "Qwen3-VL-235B", "maas-glm-4.7"}, DefaultMadaoModelIDs())
	require.Error(t, validateBuiltinChatCredentials(PlatformMadao, AccountTypeOAuth, credentials))
	credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateBuiltinChatCredentials(PlatformMadao, AccountTypeAPIKey, credentials))
}

// 码道网页登录：适配器以 poll 模式启动，服务层转发时不泄漏共享密钥或凭据。
func TestMadaoBrowserLoginProxyDoesNotExposeSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer shared-key", r.Header.Get("Authorization"))
		require.Equal(t, "admin:42", r.Header.Get("X-Login-Owner"))
		require.Equal(t, "/internal/login/sessions", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": strings.Repeat("c", 64),
			"auth_url":   "https://devcloud.cn-north-4.huaweicloud.com/chat/login",
			"mode":       "poll", "status": "pending", "expires_at": 1,
		})
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, MadaoURL: server.URL, MadaoKey: "shared-key",
	})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	result, err := BuiltinAdapterLogin(context.Background(), PlatformMadao, "admin:42", "", "start", "")
	require.NoError(t, err)
	require.Equal(t, "poll", result.Mode)
	require.NotContains(t, result.AuthURL, "shared-key")
}

func TestMadaoLoginViewProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer shared-key", r.Header.Get("Authorization"))
		require.Equal(t, "/internal/login/sessions/"+strings.Repeat("c", 64)+"/view", r.URL.Path)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, MadaoURL: server.URL, MadaoKey: "shared-key",
	})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	view, err := BuiltinAdapterLoginView(context.Background(), PlatformMadao, "admin:42", strings.Repeat("c", 64))
	require.NoError(t, err)
	require.Equal(t, "image/png", view.ContentType)
	require.Equal(t, []byte("png"), view.Body)
}
