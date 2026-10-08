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
		CallbackURL string   `json:"callback_url"`
		Email       string   `json:"email"`
		Password    string   `json:"password"`
		Plan        string   `json:"plan"`
		Provider    string   `json:"provider"`
		AutoRelogin bool     `json:"auto_relogin"`
		Type        string   `json:"type"`
		X           float64  `json:"x"`
		Y           float64  `json:"y"`
		DeltaX      float64  `json:"delta_x"`
		DeltaY      float64  `json:"delta_y"`
		Text        string   `json:"text"`
		Key         string   `json:"key"`
	}
	action := c.Param("action")
	if c.Param("session") == "" {
		action = "start"
	}
	if c.Request.Method == http.MethodDelete {
		action = "cancel"
	}
	// 截图式登录：把页面交互转发给隔离浏览器。
	if action == "input" {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		if err := c.ShouldBindJSON(&body); err != nil {
			response.BadRequest(c, "Invalid input event")
			return
		}
		event := service.BuiltinLoginInputEvent{Type: body.Type, X: body.X, Y: body.Y, DeltaX: body.DeltaX, DeltaY: body.DeltaY, Text: body.Text, Key: body.Key}
		if err := service.BuiltinAdapterLoginInput(c.Request.Context(), c.Param("platform"), owner, c.Param("session"), event); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		response.Success(c, map[string]bool{"ok": true})
		return
	}
	if action == "callback" {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		if err := c.ShouldBindJSON(&body); err != nil || body.CallbackURL == "" {
			response.BadRequest(c, "Callback URL is required")
			return
		}
	}
	if action == "start" && (c.Param("platform") == service.PlatformZcode || c.Param("platform") == service.PlatformArena || c.Param("platform") == service.PlatformDeepseekWeb || c.Param("platform") == service.PlatformCursor) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
		if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
			response.BadRequest(c, "Invalid login options")
			return
		}
	}
	options := service.BuiltinLoginOptions{Plan: body.Plan, Provider: body.Provider, Email: body.Email, Password: body.Password, AutoRelogin: body.AutoRelogin}
	result, err := service.BuiltinAdapterLogin(c.Request.Context(), c.Param("platform"), owner, c.Param("session"), action, body.CallbackURL, options)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, result)
}
