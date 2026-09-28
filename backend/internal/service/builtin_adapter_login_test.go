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

func TestBuiltinAdapterLoginTrustedDestinationAndSanitizedResponse(t *testing.T) {
	id := strings.Repeat("a", 64)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "Bearer internal-key", r.Header.Get("Authorization"))
		require.Equal(t, "admin:42", r.Header.Get("X-Login-Owner"))
		require.Empty(t, r.URL.RawQuery)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "secret-callback", body["callback_url"])
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "status": "completed", "mode": "callback", "access_token": "must-not-leak", "account": map[string]string{"uid": "u1", "refresh_token": "must-not-leak"}})
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, TraeworkURL: server.URL + "/v1", TraeworkKey: "internal-key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	result, err := BuiltinAdapterLogin(context.Background(), PlatformTraework, "admin:42", id, "callback", "secret-callback")
	require.NoError(t, err)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "must-not-leak")
	_, err = BuiltinAdapterLogin(context.Background(), "https://evil.test", "admin:42", id, "callback", "")
	require.Error(t, err)
	_, err = BuiltinAdapterLogin(context.Background(), PlatformTraework, "admin:42", "../../status", "poll", "")
	require.Error(t, err)
	require.Equal(t, 1, requests)
}

func TestWorkbuddyBuiltinCredentialsInjected(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	credentials := map[string]any{}
	applyBuiltinAdapterCredentials(PlatformWorkbuddy, credentials)
	require.Equal(t, "http://sub2api-workbuddy:7863/v1", credentials["base_url"])
	require.Equal(t, "builtin-workbuddy-key", credentials["api_key"])
	require.NoError(t, validateBuiltinChatCredentials(PlatformWorkbuddy, AccountTypeAPIKey, credentials))
}

// ZCode 通过内置适配器完成网页授权，登录会话接口必须可达且使用适配器密钥。
func TestZcodeBuiltinAdapterLoginReachable(t *testing.T) {
	id := strings.Repeat("b", 64)
	var gotAuth, gotOwner string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotOwner = r.Header.Get("X-Login-Owner")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": id, "status": "pending", "mode": "poll",
			"auth_url": "https://chat.z.ai/api/oauth/authorize?client_id=test", "expires_at": 1,
		})
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, ZcodeURL: server.URL + "/v1", ZcodeKey: "zcode-internal-key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	result, err := BuiltinAdapterLogin(context.Background(), PlatformZcode, "admin:7", "", "start", "")
	require.NoError(t, err)
	require.Equal(t, "Bearer zcode-internal-key", gotAuth)
	require.Equal(t, "admin:7", gotOwner)
	require.Equal(t, "poll", result.Mode)
	require.Contains(t, result.AuthURL, "chat.z.ai")
}

func TestZcodeLoginPlanOptionsForwardedAndValidated(t *testing.T) {
	id := strings.Repeat("c", 64)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "start-plan", body["plan"])
		require.Equal(t, "bigmodel", body["provider"])
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "status": "pending", "mode": "poll"})
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, ZcodeURL: server.URL, ZcodeKey: "key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	_, err := BuiltinAdapterLogin(context.Background(), PlatformZcode, "admin:1", "", "start", "", BuiltinLoginOptions{Plan: "start-plan", Provider: "bigmodel"})
	require.NoError(t, err)
	_, err = BuiltinAdapterLogin(context.Background(), PlatformZcode, "admin:1", "", "start", "", BuiltinLoginOptions{Plan: "unsupported", Provider: "bigmodel"})
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestZcodeLoginErrorsDistinguishDeniedAndExpired(t *testing.T) {
	for _, tc := range []struct {
		status  int
		message string
	}{
		{http.StatusForbidden, "ZCode authorization failed or the selected plan has no active entitlement"},
		{http.StatusGone, "ZCode login expired"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"token":"must-not-leak"}`))
		}))
		SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, ZcodeURL: server.URL, ZcodeKey: "key"})
		_, err := BuiltinAdapterLogin(context.Background(), PlatformZcode, "admin:1", strings.Repeat("a", 64), "poll", "")
		require.ErrorContains(t, err, tc.message)
		require.NotContains(t, err.Error(), "must-not-leak")
		server.Close()
	}
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
}
