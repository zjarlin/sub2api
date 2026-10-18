package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RunBuiltinAdapterPlatformCheckin 立即为某个内置适配器（WorkBuddy / TRAE Work）
// 账号池里的全部登录账号执行一次签到，返回签到后的积分与历史。
//
// 与逐账号签到（/accounts/:id/sidecar/checkin）的区别：这里不要求 sub2api 账号
// 已绑定上游 uid —— 管理端要在「还没建账号 / 还没绑定」时先签到再关联，否则会
// 因为共享池里的账号当天没签到而关联到一个 0 积分账号。
//
// POST /api/v1/admin/builtin-adapters/:platform/checkin
func (h *AccountHandler) RunBuiltinAdapterPlatformCheckin(c *gin.Context) {
	platform := strings.TrimSpace(c.Param("platform"))
	overview, err := service.BuiltinAdapterPlatformCheckin(c.Request.Context(), platform)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, overview)
}
