package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesZcodeUsesOpenAIGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(1)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformZcode},
		})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	RegisterGatewayRoutes(router, &handler.Handlers{
		Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{},
		AsyncImage: handler.NewAsyncImageHandler(nil, nil),
	}, auth, nil, nil, nil, nil, nil, &config.Config{
		Gateway: config.GatewayConfig{MaxBodySize: 1024, TextMaxBodySize: 1024},
	})

	for _, path := range []string{
		"/v1/responses", "/responses", "/backend-api/codex/responses",
		"/v1/responses/compact", "/responses/compact",
		"/v1/messages", "/v1/chat/completions", "/chat/completions",
		"/v1/messages/count_tokens", "/messages/count_tokens",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, nil)
			router.ServeHTTP(response, request)
			// 空依赖的 OpenAI handler 在读取请求体前返回 503；走到其他 handler 会先返回空请求体错误。
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "Service temporarily unavailable")
		})
	}
}
