package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// v0 分组只能服务原型端点，否则 auto 选路会把文本请求打到 v0 账号。
func TestV0GroupsRejectNonPrototypeEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func(platform string) *gin.Engine {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: platform}})
			c.Next()
		})
		router.Use(rejectNonPrototypeEndpointsForV0Groups)
		router.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })
		router.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
		router.POST("/v1/responses", func(c *gin.Context) { c.Status(http.StatusOK) })
		router.Any("/v1/v0/*proxyPath", func(c *gin.Context) { c.Status(http.StatusOK) })
		return router
	}

	v0Router := newRouter(service.PlatformV0)
	for _, testCase := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/v1/chat/completions", http.StatusBadRequest},
		{http.MethodPost, "/v1/responses", http.StatusBadRequest},
		{http.MethodGet, "/v1/models", http.StatusOK},
		{http.MethodGet, "/v1/v0/chats", http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		v0Router.ServeHTTP(recorder, httptest.NewRequest(testCase.method, testCase.path, nil))
		require.Equal(t, testCase.want, recorder.Code, testCase.path)
	}

	openAIRouter := newRouter(service.PlatformOpenAI)
	recorder := httptest.NewRecorder()
	openAIRouter.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
}
