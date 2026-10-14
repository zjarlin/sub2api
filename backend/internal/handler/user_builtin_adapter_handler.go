package handler

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 用户侧内置适配器接口：与管理员接口共用同一套部署级适配器与共享登录账号池，
// 但走 JWT 用户鉴权、按登录用户隔离会话归属。管理员入口保持不变。
//
// 归属作用域使用 "user:<uid>"，仅用于适配器侧会话隔离，不参与账号授权判断。

const userBuiltinLoginOwnerPrefix = "user:"

const maxDoubaoSessionSnapshotBytes = 64 << 10

var doubaoSessionProfileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func userBuiltinLoginOwner(c *gin.Context) (string, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return "", false
	}
	return userBuiltinLoginOwnerPrefix + strconv.FormatInt(subject.UserID, 10), true
}

// UserBuiltinAdapterLogin 处理内置适配器登录会话的建立、轮询/回调、取消与截图查看。
// POST   /api/v1/user/builtin-adapters/:platform/login-sessions
// POST   /api/v1/user/builtin-adapters/:platform/login-sessions/:session/:action
// GET    /api/v1/user/builtin-adapters/:platform/login-sessions/:session/view
// DELETE /api/v1/user/builtin-adapters/:platform/login-sessions/:session
func (h *UserAccountHandler) BuiltinAdapterLogin(c *gin.Context) {
	owner, ok := userBuiltinLoginOwner(c)
	if !ok {
		return
	}
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
		CallbackURL string  `json:"callback_url"`
		Email       string  `json:"email"`
		Password    string  `json:"password"`
		Plan        string  `json:"plan"`
		Provider    string  `json:"provider"`
		AutoRelogin bool    `json:"auto_relogin"`
		Type        string  `json:"type"`
		X           float64 `json:"x"`
		Y           float64 `json:"y"`
		DeltaX      float64 `json:"delta_x"`
		DeltaY      float64 `json:"delta_y"`
		Text        string  `json:"text"`
		Key         string  `json:"key"`
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

// UserListDoubaoSessionProfiles 返回已上传的豆包会话别名（脱敏，只读共享池）。
// GET /api/v1/user/builtin-adapters/doubao/profiles
func (h *UserAccountHandler) ListDoubaoSessionProfiles(c *gin.Context) {
	profiles, err := service.ListDoubaoSessionProfiles(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"profiles": profiles})
}

// UserUploadDoubaoSessionProfile 接收 Mac 端导出的豆包 session.json 快照。
// POST /api/v1/user/builtin-adapters/doubao/profiles
func (h *UserAccountHandler) UploadDoubaoSessionProfile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDoubaoSessionSnapshotBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		response.BadRequest(c, "上传请求无效或超过大小限制")
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	profile := strings.TrimSpace(c.PostForm("profile"))
	if !doubaoSessionProfileName.MatchString(profile) || profile == "default" {
		response.BadRequest(c, "会话名称仅支持字母、数字、短横线和下划线，且不能使用 default")
		return
	}
	file, _, err := c.Request.FormFile("session")
	if err != nil {
		response.BadRequest(c, "请选择有效的豆包 session.json 文件")
		return
	}
	defer file.Close()
	snapshot, err := io.ReadAll(io.LimitReader(file, maxDoubaoSessionSnapshotBytes+1))
	if err != nil {
		response.BadRequest(c, "无法读取豆包会话文件")
		return
	}
	if len(snapshot) > maxDoubaoSessionSnapshotBytes {
		response.BadRequest(c, "豆包会话文件不能超过 64 KiB")
		return
	}
	if err := service.UploadDoubaoSessionProfile(c.Request.Context(), profile, snapshot); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"name": profile})
}

// UserDeleteDoubaoSessionProfile 删除指定的豆包会话快照。
// DELETE /api/v1/user/builtin-adapters/doubao/profiles/:profile
func (h *UserAccountHandler) DeleteDoubaoSessionProfile(c *gin.Context) {
	profile := strings.TrimSpace(c.Param("profile"))
	if err := service.DeleteDoubaoSessionProfile(c.Request.Context(), profile); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"name": profile, "deleted": true})
}

// UserGetBuiltinAdapterCheckins 返回当前用户自有账号对应内置适配器（WorkBuddy / TRAE Work）
// 账号池的积分与签到历史（脱敏，只读）。
// GET /api/v1/user/accounts/:id/sidecar/checkins
func (h *UserAccountHandler) GetBuiltinAdapterCheckins(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	accountID, ok := parseUserAccountID(c)
	if !ok {
		return
	}
	account, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), accountID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	overview, err := service.BuiltinAdapterCheckinOverviewForPlatform(c.Request.Context(), account.Platform)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, overview)
}

// UserGetVibexUsage 返回当前用户自有 VibeX 账号的余额与额度。
// GET /api/v1/user/accounts/:id/vibex-usage
func (h *UserAccountHandler) GetVibexUsage(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	accountID, ok := parseUserAccountID(c)
	if !ok {
		return
	}
	account, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), accountID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !account.IsVibex() {
		response.BadRequest(c, "VibeX usage requires a VibeX account")
		return
	}
	result, err := service.FetchVibexUsage(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, result)
}
