package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetModelFallbackPolicy(c *gin.Context) {
	policy, err := h.settingService.GetModelFallbackPolicy(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, policy)
}

func (h *SettingHandler) GetModelFallbackPreset(c *gin.Context) {
	response.Success(c, service.DefaultModelFallbackPolicy())
}

func (h *SettingHandler) UpdateModelFallbackPolicy(c *gin.Context) {
	var policy service.ModelFallbackPolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := policy.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.settingService.SetModelFallbackPolicy(c.Request.Context(), &policy); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, policy)
}
