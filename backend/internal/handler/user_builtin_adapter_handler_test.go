//go:build unit

package handler

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

func TestTeleagentUserBuiltinLoginRoutesPreserveScope(t *testing.T) {
	sessionID := strings.Repeat("a", 64)
	localKey := "ta-" + strings.Repeat("b", 64)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer shared-key", r.Header.Get("Authorization"))
		if r.Header.Get("X-Login-Owner") != "user:22" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/view") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/input") {
			var event service.BuiltinLoginInputEvent
			require.NoError(t, json.NewDecoder(r.Body).Decode(&event))
			require.Equal(t, "text", event.Type)
			require.Equal(t, "hello", event.Text)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "mode": "poll", "status": "completed", "api_key": localKey, "account": map[string]string{"uid": "cloud-uid", "nickname": "user", "token": "cloud-secret"}})
	}))
	defer adapter.Close()
	service.SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, TeleagentURL: adapter.URL, TeleagentKey: "shared-key"})
	t.Cleanup(func() { service.SetBuiltinAdapterConfig(nil) })
	h := &UserAccountHandler{}
	base := "/builtin-adapters/teleagent/login-sessions"
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, base, `{}`},
		{http.MethodPost, base + "/" + sessionID + "/poll", `{}`},
		{http.MethodGet, base + "/" + sessionID + "/view", ""},
		{http.MethodPost, base + "/" + sessionID + "/input", `{"type":"text","text":"hello"}`},
	} {
		result := userBuiltinAdapterRequest(h, tc.method, tc.path, tc.body, 22)
		require.Equal(t, http.StatusOK, result.Code, result.Body.String())
		require.Equal(t, "no-store", result.Header().Get("Cache-Control"))
		require.NotContains(t, result.Body.String(), "cloud-secret")
		require.NotContains(t, result.Body.String(), "shared-key")
		if strings.HasSuffix(tc.path, "/view") {
			require.Equal(t, "image/png", result.Header().Get("Content-Type"))
		}
		if tc.path == base {
			require.Contains(t, result.Body.String(), localKey)
		}
		denied := userBuiltinAdapterRequest(h, tc.method, tc.path, tc.body, 23)
		require.Equal(t, http.StatusNotFound, denied.Code)
	}
}

// 用户侧内置适配器路由：必须走 JWT 用户鉴权，且不得依赖管理员角色。
// 未登录调用在触碰任何账号/适配器前即被拒绝。
func userBuiltinAdapterRequest(h *UserAccountHandler, method, path, body string, user int64) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if user > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user})
		}
		c.Next()
	})
	login := router.Group("/builtin-adapters/:platform/login-sessions")
	login.POST("", h.BuiltinAdapterLogin)
	login.POST("/:session/:action", h.BuiltinAdapterLogin)
	login.GET("/:session/view", h.BuiltinAdapterLogin)
	login.DELETE("/:session", h.BuiltinAdapterLogin)
	router.GET("/accounts/:id/sidecar/checkins", h.GetBuiltinAdapterCheckins)
	router.GET("/accounts/:id/vibex-usage", h.GetVibexUsage)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	return rec
}

func TestUserBuiltinAdapterLoginRequiresAuthenticatedUser(t *testing.T) {
	h := &UserAccountHandler{}
	// 未登录：401，且不会向后端适配器发起任何请求。
	result := userBuiltinAdapterRequest(h, http.MethodPost, "/builtin-adapters/workbuddy/login-sessions", `{}`, 0)
	require.Equal(t, http.StatusUnauthorized, result.Code)
}

func TestUserBuiltinAdapterLoginRejectsUnsupportedPlatformForAuthenticatedUser(t *testing.T) {
	h := &UserAccountHandler{}
	// 已登录但平台不在内置适配器清单：走服务层校验，返回 400（非 403 权限错误）。
	result := userBuiltinAdapterRequest(h, http.MethodPost, "/builtin-adapters/unknown/login-sessions", `{}`, 22)
	require.Equal(t, http.StatusBadRequest, result.Code)
	require.NotContains(t, result.Body.String(), "Admin access required")
}

func TestUserBuiltinAdapterCheckinsRequiresOwnership(t *testing.T) {
	owner := int64(11)
	repo := &ownedAccountRepoStub{account: &service.Account{ID: 3, OwnerUserID: &owner, Platform: service.PlatformWorkbuddy}}
	h := &UserAccountHandler{accountRepo: repo}

	// 非归属用户读取他人账号签到：404，且不会调用适配器。
	result := userBuiltinAdapterRequest(h, http.MethodGet, "/accounts/3/sidecar/checkins", "", 22)
	require.Equal(t, http.StatusNotFound, result.Code)

	// 未登录：401。
	result = userBuiltinAdapterRequest(h, http.MethodGet, "/accounts/3/sidecar/checkins", "", 0)
	require.Equal(t, http.StatusUnauthorized, result.Code)
}

func TestUserVibexUsageRequiresOwnershipAndPlatform(t *testing.T) {
	owner := int64(11)
	repo := &ownedAccountRepoStub{account: &service.Account{ID: 3, OwnerUserID: &owner, Platform: service.PlatformOpenAI}}
	h := &UserAccountHandler{accountRepo: repo}

	// 归属用户但平台不是 VibeX：400。
	result := userBuiltinAdapterRequest(h, http.MethodGet, "/accounts/3/vibex-usage", "", owner)
	require.Equal(t, http.StatusBadRequest, result.Code)

	// 非归属用户：404。
	result = userBuiltinAdapterRequest(h, http.MethodGet, "/accounts/3/vibex-usage", "", 22)
	require.Equal(t, http.StatusNotFound, result.Code)
}
