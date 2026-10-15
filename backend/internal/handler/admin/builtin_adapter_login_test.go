//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTeleagentAdminBuiltinLoginRoutesPreserveScope(t *testing.T) {
	sessionID := strings.Repeat("a", 64)
	localKey := "ta-" + strings.Repeat("b", 64)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer shared-key", r.Header.Get("Authorization"))
		require.Equal(t, "admin:7", r.Header.Get("X-Login-Owner"))
		if strings.HasSuffix(r.URL.Path, "/view") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/input") {
			var event service.BuiltinLoginInputEvent
			require.NoError(t, json.NewDecoder(r.Body).Decode(&event))
			require.Equal(t, "click", event.Type)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "mode": "poll", "status": "completed", "api_key": localKey, "account": map[string]string{"uid": "cloud-uid", "nickname": "admin", "token": "cloud-secret"}})
	}))
	defer adapter.Close()
	service.SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, TeleagentURL: adapter.URL, TeleagentKey: "shared-key"})
	t.Cleanup(func() { service.SetBuiltinAdapterConfig(nil) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	h := &AccountHandler{}
	login := router.Group("/builtin-adapters/:platform/login-sessions")
	login.POST("", h.BuiltinAdapterLogin)
	login.POST("/:session/:action", h.BuiltinAdapterLogin)
	login.GET("/:session/view", h.BuiltinAdapterLogin)
	base := "/builtin-adapters/teleagent/login-sessions"
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, base, `{}`},
		{http.MethodPost, base + "/" + sessionID + "/poll", `{}`},
		{http.MethodGet, base + "/" + sessionID + "/view", ""},
		{http.MethodPost, base + "/" + sessionID + "/input", `{"type":"click","x":12,"y":34}`},
	} {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, request)
		require.Equal(t, http.StatusOK, result.Code, result.Body.String())
		require.Equal(t, "no-store", result.Header().Get("Cache-Control"))
		require.NotContains(t, result.Body.String(), "cloud-secret")
		require.NotContains(t, result.Body.String(), "shared-key")
		if tc.path == base {
			require.Contains(t, result.Body.String(), localKey)
		}
	}
}

func TestBuiltinAdapterLoginDeepseekWebForwardsCredentials(t *testing.T) {
	sessionID := strings.Repeat("a", 64)
	forwarded := make(chan service.BuiltinLoginOptions, 1)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var options service.BuiltinLoginOptions
		if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
			http.Error(w, "Invalid login options", http.StatusBadRequest)
			return
		}
		forwarded <- options
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": sessionID, "status": "pending", "mode": "poll",
		})
	}))
	t.Cleanup(adapter.Close)
	service.SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, DeepseekWebURL: adapter.URL, DeepseekWebKey: "test-adapter-key",
	})
	t.Cleanup(func() { service.SetBuiltinAdapterConfig(nil) })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	handler := &AccountHandler{}
	router.POST("/api/v1/admin/builtin-adapters/:platform/login-sessions", handler.BuiltinAdapterLogin)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/builtin-adapters/deepseek_web/login-sessions",
		strings.NewReader(`{"email":" user@example.com ","password":" test-password ","auto_relogin":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	select {
	case options := <-forwarded:
		require.Equal(t, "user@example.com", options.Email)
		require.Equal(t, " test-password ", options.Password)
		require.True(t, options.AutoRelogin)
	default:
		t.Fatal("DeepSeek 登录凭据未转发到适配器")
	}
	require.Contains(t, recorder.Body.String(), sessionID)
	require.NotContains(t, recorder.Body.String(), "test-password")
	require.NotContains(t, recorder.Body.String(), "test-adapter-key")
}
