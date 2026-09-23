package jev_api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler 暴露 TypeSafe / JEV System One 的中继端点。
type Handler struct {
	provider TargetProvider
}

// NewHandler 构造中继 handler；provider 通常由内容审计服务适配器提供。
func NewHandler(provider TargetProvider) *Handler {
	return &Handler{provider: provider}
}

// Relay 处理 POST /v1/systemone：客户端已通过网关鉴权，这里只做原样转发。
func (h *Handler) Relay(c *gin.Context) {
	if h == nil || h.provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "TypeSafe relay is not configured"}})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, int64(maxRequestBytes)+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "failed to read request body"}})
		return
	}
	if len(body) > maxRequestBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "request body too large"}})
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "request body must be a JSON object"}})
		return
	}
	status, payload, err := Relay(c.Request.Context(), h.provider, body)
	if err != nil {
		code := http.StatusBadGateway
		message := "TypeSafe upstream unavailable"
		if errors.Is(err, ErrRelayUnavailable) {
			code = http.StatusServiceUnavailable
			message = "TypeSafe relay has no configured API key"
		}
		// 不回显上游正文，避免泄漏用户输入或凭据。
		c.JSON(code, gin.H{"error": gin.H{"type": "upstream_error", "message": message}})
		return
	}
	if status < 200 || status >= 300 {
		c.JSON(status, gin.H{"error": gin.H{"type": "upstream_error", "message": "TypeSafe upstream returned an error"}})
		return
	}
	c.Data(status, "application/json", payload)
}

// Register 把中继端点挂到已鉴权的 /v1 路由组上。
func Register(group *gin.RouterGroup, provider TargetProvider) {
	if group == nil {
		return
	}
	group.POST("/systemone", NewHandler(provider).Relay)
}
