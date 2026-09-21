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
