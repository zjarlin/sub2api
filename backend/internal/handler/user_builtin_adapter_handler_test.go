//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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
