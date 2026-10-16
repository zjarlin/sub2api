package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterWeatherRoutes 注册天气服务路由。
func RegisterWeatherRoutes(r *gin.Engine, h *handler.Handlers, apiKeyAuth middleware2.APIKeyAuthMiddleware) {
	if h.Weather == nil {
		return
	}

	v1 := r.Group("/api/v1")
	{
		// 公开端点：获取可用数据源与能力
		v1.GET("/weather/providers", h.Weather.Providers)

		// 需要 API Key 认证的天气端点
		weatherGroup := v1.Group("/weather")
		weatherGroup.Use(gin.HandlerFunc(apiKeyAuth))
		{
			weatherGroup.POST("", h.Weather.Weather)
		}
	}
}
