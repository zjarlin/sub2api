package admin

import (
	"errors"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BuiltinAdapterLogin 使用管理员身份隔离登录会话，不将回调凭据写入账号表或日志。
func (h *AccountHandler) BuiltinAdapterLogin(c *gin.Context) {
	owner := adminActorScope(c)
	if c.Request.Method == http.MethodGet {
		view, err := service.BuiltinAdapterLoginView(c.Request.Context(), c.Param("platform"), owner, c.Param("session"))
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Type", view.ContentType)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, view.ContentType, view.Body)
		return
	}
	var body struct {
		CallbackURL string `json:"callback_url"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		Plan        string `json:"plan"`
		Provider    string `json:"provider"`
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
	if action == "start" && (c.Param("platform") == service.PlatformZcode || c.Param("platform") == service.PlatformArena || c.Param("platform") == service.PlatformDeepseekWeb) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
		if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
			response.BadRequest(c, "Invalid login options")
			return
		}
	}
	options := service.BuiltinLoginOptions{Plan: body.Plan, Provider: body.Provider, Email: body.Email, Password: body.Password}
	result, err := service.BuiltinAdapterLogin(c.Request.Context(), c.Param("platform"), owner, c.Param("session"), action, body.CallbackURL, options)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, result)
}
