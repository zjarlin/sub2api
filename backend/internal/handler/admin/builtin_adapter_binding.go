package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BindBuiltinAdapterAccount 复用适配器内已有登录凭据，只增量保存当前记录的身份关联。
func (h *AccountHandler) BindBuiltinAdapterAccount(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var input struct {
		UID string `json:"uid" binding:"required,max=128"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.UID) == "" {
		response.BadRequest(c, "Upstream account UID is required")
		return
	}
	ctx := c.Request.Context()
	account, err := h.adminService.GetAccount(ctx, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 先核实该 UID 仍在对应平台的登录池中，不能把失效或其他平台的身份写入记录。
	updates := map[string]any{"builtin_adapter_uid": strings.TrimSpace(input.UID)}
	bound := *account
	bound.Extra = updates
	if _, err := service.BuiltinAdapterCheckinOverviewForAccount(ctx, &bound); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.adminService.UpdateAccountExtra(ctx, id, updates); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updated, err := h.adminService.GetAccount(ctx, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, h.buildAccountResponseWithRuntime(ctx, updated))
}
