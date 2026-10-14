package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTranslateModelRoutesRespectAuthAndAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"translatedText":"你好"}`))
	}))
	defer upstream.Close()
	for _, allowed := range []bool{false, true} {
		for _, authenticated := range []bool{false, true} {
			router := gin.New()
			group := allowlistGroup(service.PlatformComposite, true, "auto")
			if allowed {
				group.ModelAllowlist.Models = []string{"translate"}
			}
			RegisterGatewayRoutes(router, &handler.Handlers{
				Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}, AsyncImage: handler.NewAsyncImageHandler(nil, nil),
				Translate: handler.NewTranslateHandler(translate.NewAggregator(&translate.Config{LibreTranslate: &translate.LibreTranslateConfig{BaseURL: upstream.URL}}), nil),
			}, middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
				if !authenticated {
					c.AbortWithStatus(401)
					return
				}
				id := int64(1)
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &id, Group: group})
				c.Next()
			}), nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1 << 20, TextMaxBodySize: 1 << 20}})
			for _, path := range []string{"/v1/responses", "/responses", "/api/codex/responses"} {
				// 仅验证已注册的别名。
				found := false
				for _, route := range router.Routes() {
					if route.Path == path && route.Method == "POST" {
						found = true
					}
				}
				if !found {
					continue
				}
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"translate","input":"Hello"}`))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, request)
				expected := 200
				if !authenticated {
					expected = 401
				} else if !allowed {
					expected = 404
				}
				require.Equal(t, expected, recorder.Code, recorder.Body.String())
			}
		}
	}
}
