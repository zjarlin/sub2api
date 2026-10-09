package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UserAccountRebateHandler 暴露"自带账号按真实消耗返额"的用户侧查询。
type UserAccountRebateHandler struct {
	rebateService *service.UserAccountRebateService
}

// NewUserAccountRebateHandler 创建用户侧返额查询处理器。
func NewUserAccountRebateHandler(rebateService *service.UserAccountRebateService) *UserAccountRebateHandler {
	return &UserAccountRebateHandler{rebateService: rebateService}
}

// GetAccountRebates 返回当前用户的返额概览。
// GET /api/v1/user/account-rebates
func (h *UserAccountRebateHandler) GetAccountRebates(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	summary, err := h.rebateService.GetSummary(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, summary)
}

// ListAccountRebateHistory 返回当前用户的返额流水（分页）。
// GET /api/v1/user/account-rebates/history
func (h *UserAccountRebateHandler) ListAccountRebateHistory(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.rebateService.ListHistory(c.Request.Context(), subject.UserID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}
