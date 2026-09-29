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
		strings.NewReader(`{"email":" user@example.com ","password":" test-password "}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	select {
	case options := <-forwarded:
		require.Equal(t, "user@example.com", options.Email)
		require.Equal(t, " test-password ", options.Password)
	default:
		t.Fatal("DeepSeek 登录凭据未转发到适配器")
	}
	require.Contains(t, recorder.Body.String(), sessionID)
	require.NotContains(t, recorder.Body.String(), "test-password")
	require.NotContains(t, recorder.Body.String(), "test-adapter-key")
}
