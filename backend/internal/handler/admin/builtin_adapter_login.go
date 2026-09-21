package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BuiltinAdapterLogin 使用管理员身份隔离登录会话，不将回调凭据写入账号表或日志。
func (h *AccountHandler) BuiltinAdapterLogin(c *gin.Context) {
	owner := adminActorScope(c)
	var body struct {
		CallbackURL string `json:"callback_url"`
	}
	action := c.Param("action")
	if c.Param("session") == "" {
		action = "start"
	}
	if c.Request.Method == http.MethodDelete {
		action = "cancel"
	}
	if action == "callback" {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		if err := c.ShouldBindJSON(&body); err != nil || body.CallbackURL == "" {
			response.BadRequest(c, "Callback URL is required")
			return
		}
	}
	result, err := service.BuiltinAdapterLogin(c.Request.Context(), c.Param("platform"), owner, c.Param("session"), action, body.CallbackURL)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, result)
}
