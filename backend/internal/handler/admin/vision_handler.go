package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// VisionHandler 提供视觉服务的管理状态。
type VisionHandler struct {
	cfg *config.Config
}

func NewVisionHandler(cfg *config.Config) *VisionHandler {
	return &VisionHandler{cfg: cfg}
}

// GetStatus 返回边缘视觉服务的启用状态，不暴露内部服务地址。
func (h *VisionHandler) GetStatus(c *gin.Context) {
	if h.cfg == nil {
		response.Success(c, gin.H{"enabled": false, "laya_enabled": false, "media_enabled": false})
		return
	}
	vision := h.cfg.Gateway.Vision
	response.Success(c, gin.H{
		"enabled":       vision.Enabled,
		"laya_enabled":  h.cfg.Gateway.Laya.Enabled,
		"media_enabled": h.cfg.Gateway.Media.Enabled,
	})
}
