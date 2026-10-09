package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UserAccountRebateHandler 管理端返额流水的查询与手动结算触发。
type UserAccountRebateHandler struct {
	rebateService *service.UserAccountRebateService
}

// NewUserAccountRebateHandler 创建管理端返额处理器。
func NewUserAccountRebateHandler(rebateService *service.UserAccountRebateService) *UserAccountRebateHandler {
	return &UserAccountRebateHandler{rebateService: rebateService}
}

// ListRecords 返回全部返额流水（分页，支持按邮箱/用户名/账号 ID 搜索）。
// GET /api/v1/admin/account-rebates/records
func (h *UserAccountRebateHandler) ListRecords(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.rebateService.ListAllRecords(c.Request.Context(), c.Query("search"), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

// RunSettle 手动触发一轮结算，便于灰度验证（总开关关闭时返回 skipped）。
// POST /api/v1/admin/account-rebates/settle
func (h *UserAccountRebateHandler) RunSettle(c *gin.Context) {
	result, err := h.rebateService.RunOnce(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
