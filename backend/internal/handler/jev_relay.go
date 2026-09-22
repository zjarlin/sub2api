package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/jev_api"

	"github.com/gin-gonic/gin"
)

// RegisterJevRelay 把 TypeSafe / JEV System One 中继挂到已鉴权的路由组上。
//
// 这是上游 routes 包唯一需要调用的入口；JEV 的实现、配置读取与转发逻辑
// 全部位于 internal/jev_api，避免与上游文件产生合并冲突。
func (h *GatewayHandler) RegisterJevRelay(group *gin.RouterGroup) {
	if h == nil || h.contentModerationService == nil {
		return
	}
	jev_api.Register(group, h.contentModerationService)
}
