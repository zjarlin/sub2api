package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// QoderOAuthHandler 暴露 Qoder 设备流授权（Device Flow）管理端接口。
type QoderOAuthHandler struct {
	qoderOAuthService *service.QoderOAuthService
}

func NewQoderOAuthHandler(qoderOAuthService *service.QoderOAuthService) *QoderOAuthHandler {
	return &QoderOAuthHandler{qoderOAuthService: qoderOAuthService}
}

// GenerateAuthURL 生成设备授权链接。
func (h *QoderOAuthHandler) GenerateAuthURL(c *gin.Context) {
	result, err := h.qoderOAuthService.GenerateAuthURL(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type QoderPollTokenRequest struct {
	SessionID string `json:"session_id" binding:"required"`
}

// PollToken 轮询设备授权结果，未完成时返回 done=false。
func (h *QoderOAuthHandler) PollToken(c *gin.Context) {
	var req QoderPollTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	token, done, err := h.qoderOAuthService.PollToken(c.Request.Context(), strings.TrimSpace(req.SessionID))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !done {
		response.Success(c, gin.H{"done": false})
		return
	}
	response.Success(c, gin.H{"done": true, "token": token})
}

type QoderRefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshToken 使用 refresh_token 续期设备令牌。
func (h *QoderOAuthHandler) RefreshToken(c *gin.Context) {
	var req QoderRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	token, err := h.qoderOAuthService.RefreshToken(c.Request.Context(), strings.TrimSpace(req.RefreshToken))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, token)
}
