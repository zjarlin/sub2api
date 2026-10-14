package routes

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 用户侧内置适配器路由必须挂在 /api/v1/user 下并只要求登录用户，
// 不能依赖管理员角色（否则「我的账号」页面里的 WorkBuddy 等登录会拿到 403）。
func TestUserBuiltinAdapterRoutesMountedUnderUserWithoutAdminRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{UserAccount: &handler.UserAccountHandler{}}
	jwtAuth := servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		// 模拟普通登录用户：不设置管理员角色。
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 22})
		c.Next()
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	RegisterUserRoutes(router.Group("/api/v1"), handlers, jwtAuth, auditLog, &service.SettingService{}, nil)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/user/builtin-adapters/workbuddy/login-sessions"},
		{http.MethodGet, "/api/v1/user/builtin-adapters/doubao/profiles"},
		{http.MethodGet, "/api/v1/user/accounts/1/sidecar/checkins"},
		{http.MethodGet, "/api/v1/user/accounts/1/vibex-usage"},
	} {
		t.Run(tc.method+" "+tc.path+"/unauthenticated", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, nil)
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
	}
}

// 源码守卫：内置适配器前端 API 必须按作用域拼接路径，
// 不能把 /admin/builtin-adapters 写死（否则普通用户「我的账号」必然 403）。
func TestBuiltinAdapterAPIPathsAreScoped(t *testing.T) {
	source, err := os.ReadFile("../../../../frontend/src/api/admin/builtinAdapters.ts")
	require.NoError(t, err)
	text := string(source)
	require.Contains(t, text, "'/user'")
	require.Contains(t, text, "'/admin'")
	// 不允许出现写死的管理员前缀字面量（拼接模板 `${dbl(scope)}...` 除外）。
	require.NotContains(t, text, "apiClient.get<DoubaoSessionProfiles>('/admin")
	require.NotContains(t, text, "apiClient.get<VibexUsage>(`/admin/accounts")
}
