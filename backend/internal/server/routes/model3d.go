package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterModel3DRoutes 注册服务商发现与需要密钥认证的异步任务接口。
func RegisterModel3DRoutes(r *gin.Engine, h *handler.Handlers, auth middleware.APIKeyAuthMiddleware) {
	if h.Model3D == nil {
		return
	}
	g := r.Group("/api/v1/3d")
	g.GET("/providers", h.Model3D.Providers)
	g.Use(gin.HandlerFunc(auth))
	g.POST("/generations", h.Model3D.Create)
	g.GET("/tasks/:task_id", h.Model3D.Status)
	g.DELETE("/tasks/:task_id", h.Model3D.Cancel)
	g.GET("/tasks/:task_id/assets/:index", h.Model3D.Asset)
	g.GET("/balance", h.Model3D.Balance)
}
