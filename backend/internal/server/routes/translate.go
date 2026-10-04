package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterTranslateRoutes 注册翻译服务路由
func RegisterTranslateRoutes(r *gin.Engine, h *handler.Handlers, apiKeyAuth middleware2.APIKeyAuthMiddleware) {
	if h.Translate == nil {
		return
	}

	v1 := r.Group("/api/v1")
	{
		// 公开端点：获取可用服务商列表
		v1.GET("/translate/providers", h.Translate.Providers)

		// 需要 API Key 认证的翻译端点
		translateGroup := v1.Group("/translate")
		translateGroup.Use(gin.HandlerFunc(apiKeyAuth))
		{
			translateGroup.POST("", h.Translate.Translate)
		}
	}
}
