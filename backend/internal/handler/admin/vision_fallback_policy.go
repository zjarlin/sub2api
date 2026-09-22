package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetVisionFallbackPolicy(c *gin.Context) {
	policy, err := h.settingService.GetVisionFallbackPolicy(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, policy)
}

func (h *SettingHandler) UpdateVisionFallbackPolicy(c *gin.Context) {
	var policy service.VisionFallbackPolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := policy.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.settingService.SetVisionFallbackPolicy(c.Request.Context(), &policy); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, policy)
}
