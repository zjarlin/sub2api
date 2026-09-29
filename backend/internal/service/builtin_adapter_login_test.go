//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
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

func TestArenaWebLoginUsesInternalCredentials(t *testing.T) {
	id := strings.Repeat("d", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/login/sessions", r.URL.Path)
		require.Equal(t, "Bearer internal-arena", r.Header.Get("Authorization"))
		require.Equal(t, "admin:7", r.Header.Get("X-Login-Owner"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		require.Equal(t, " private-password ", body["password"])
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": id, "mode": "poll", "status": "completed",
			"password": "private-password", "account": map[string]string{"uid": "user", "model_id": "arena-session-new", "cookie": "private-cookie"},
		})
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, ArenaURL: server.URL, ArenaKey: "internal-arena"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	result, err := BuiltinAdapterLogin(context.Background(), PlatformArena, "admin:7", "", "start", "", BuiltinLoginOptions{Email: " user@example.com ", Password: " private-password "})
	require.NoError(t, err)
	require.Equal(t, "arena-session-new", result.Account.ModelID)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private")
	_, err = BuiltinAdapterLogin(context.Background(), PlatformArena, "admin:7", "", "start", "")
	require.Error(t, err)
	credentials := map[string]any{}
	applyBuiltinAdapterCredentials(PlatformArena, credentials)
	require.Equal(t, server.URL+"/v1", credentials["base_url"])
	require.Equal(t, "internal-arena", credentials["api_key"])
	require.NoError(t, validateBuiltinChatCredentials(PlatformArena, AccountTypeAPIKey, credentials))
}

func TestArenaLoginFailureCodesAreSanitized(t *testing.T) {
	for _, tc := range []struct {
		code   string
		reason string
		status int
	}{
		{"arena_access_blocked", "ARENA_ACCESS_BLOCKED", http.StatusServiceUnavailable},
		{"login_invalid_credentials", "ARENA_INVALID_CREDENTIALS", http.StatusBadRequest},
		{"session_not_usable", "ARENA_SESSION_UNUSABLE", http.StatusForbidden},
		{"session_not_ready", "ARENA_SESSION_NOT_READY", http.StatusBadGateway},
		{"browser_unavailable", "ARENA_BROWSER_UNAVAILABLE", http.StatusServiceUnavailable},
		{"arena_network_error", "ARENA_NETWORK_ERROR", http.StatusBadGateway},
		{"arena_login_timeout", "ARENA_LOGIN_TIMEOUT", http.StatusGatewayTimeout},
		{"arena_session_prepare_failed", "ARENA_SESSION_PREPARE_FAILED", http.StatusBadGateway},
	} {
		t.Run(tc.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"code": tc.code, "message": "private-password private-cookie"},
				})
			}))
			defer server.Close()
			SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, ArenaURL: server.URL, ArenaKey: "private-key"})
			t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
			_, err := BuiltinAdapterLogin(context.Background(), PlatformArena, "admin:1", strings.Repeat("a", 64), "poll", "")
			require.Error(t, err)
			require.Equal(t, tc.reason, infraerrors.Reason(err))
			require.Equal(t, tc.status, infraerrors.Code(err))
			require.NotContains(t, err.Error(), "private")
		})
	}
}

func TestArenaUnknownLoginFailuresDoNotInvalidateAdminAuth(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"private-unknown-code","message":"private-cookie"}}`,
		`<html>private-cookie</html>`,
		`{"error":{"message":"` + strings.Repeat("x", 16<<10) + `","code":"arena_access_blocked"}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(body))
		}))
		SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, ArenaURL: server.URL, ArenaKey: "private-key"})
		_, err := BuiltinAdapterLogin(context.Background(), PlatformArena, "admin:1", strings.Repeat("a", 64), "poll", "")
		require.Error(t, err)
		require.Equal(t, "ADAPTER_LOGIN_FAILED", infraerrors.Reason(err))
		require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
		require.NotContains(t, err.Error(), "private")
		server.Close()
	}
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
}

func TestDeepseekWebLoginAutoReloginOptionsAndSanitizedStatus(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, enabled, body["auto_relogin"])
				require.Equal(t, "user@example.com", body["email"])
				require.Equal(t, "private-password", body["password"])
				_ = json.NewEncoder(w).Encode(map[string]any{
					"session_id": strings.Repeat("e", 64), "status": "completed", "mode": "poll",
					"account": map[string]any{"uid": "user", "auto_relogin": enabled, "password": "private-password", "token": "private-token"},
				})
			}))
			defer server.Close()
			SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, DeepseekWebURL: server.URL, DeepseekWebKey: "private-key"})
			t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
			result, err := BuiltinAdapterLogin(context.Background(), PlatformDeepseekWeb, "admin:7", "", "start", "", BuiltinLoginOptions{
				Email: " user@example.com ", Password: "private-password", AutoRelogin: enabled,
			})
			require.NoError(t, err)
			require.NotNil(t, result.Account.AutoRelogin)
			require.Equal(t, enabled, *result.Account.AutoRelogin)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			require.Contains(t, string(raw), fmt.Sprintf(`"auto_relogin":%t`, enabled))
			require.NotContains(t, string(raw), "private-")
		})
	}
}
