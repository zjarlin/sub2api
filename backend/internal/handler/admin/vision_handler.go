package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"

	"github.com/gin-gonic/gin"
)

// VisionHandler 提供边缘服务的启用状态。
type VisionHandler struct {
	cfg        *config.Config
	translator *translate.Aggregator
}

func NewVisionHandler(cfg *config.Config, translator *translate.Aggregator) *VisionHandler {
	return &VisionHandler{cfg: cfg, translator: translator}
}

// translateProviders 返回已配置的翻译服务商，未配置时返回空列表。
func (h *VisionHandler) translateProviders() []string {
	if h.translator == nil {
		return []string{}
	}
	return h.translator.AvailableProviders()
}

// GetStatus 返回边缘视觉服务的启用状态，不暴露内部服务地址。
func (h *VisionHandler) GetStatus(c *gin.Context) {
	providers := h.translateProviders()
	if h.cfg == nil {
		response.Success(c, gin.H{
			"enabled":             false,
			"laya_enabled":        false,
			"media_enabled":       false,
			"translate_enabled":   len(providers) > 0,
			"translate_providers": providers,
		})
		return
	}
	vision := h.cfg.Gateway.Vision
	response.Success(c, gin.H{
		"enabled":             vision.Enabled,
		"laya_enabled":        h.cfg.Gateway.Laya.Enabled,
		"media_enabled":       h.cfg.Gateway.Media.Enabled,
		"translate_enabled":   len(providers) > 0,
		"translate_providers": providers,
	})
}
