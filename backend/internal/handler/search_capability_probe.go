package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"strconv"
)

// 仅由管理员路由注册，能力探查不借助用户 Auto 请求来猜测候选。
func (h *OpenAIGatewayHandler) SearchProbeCandidates(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "A positive group_id is required")
		return
	}
	candidates, err := h.gatewayService.SearchProbeCandidates(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, candidates)
}

func (h *OpenAIGatewayHandler) ProbeSearchCapability(c *gin.Context) {
	var input struct {
		GroupID   int64  `json:"group_id" binding:"required,gt=0"`
		AccountID int64  `json:"account_id" binding:"required,gt=0"`
		Model     string `json:"model" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.gatewayService.ProbeSearchCapability(c.Request.Context(), c, input.GroupID, input.AccountID, input.Model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
