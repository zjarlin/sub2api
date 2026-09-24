package handler

import (
	"bytes"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/jev_api"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterJevRelay 把 TypeSafe / JEV System One 中继挂到已鉴权的路由组上。
//
// 这是上游 routes 包唯一需要调用的入口；JEV 的实现、配置读取与转发逻辑
// 全部位于 internal/jev_api，避免与上游文件产生合并冲突。
func (h *GatewayHandler) RegisterJevRelay(group *gin.RouterGroup) {
	if h == nil {
		return
	}
	group.POST("/systemone", func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil || key.Group == nil || key.Group.Platform != service.PlatformOpenAI {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "System One is only available to OpenAI groups"}})
			return
		}
		if h.billingCacheService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Billing eligibility unavailable"}})
			return
		}
		subscription, _ := middleware.GetSubscriptionFromContext(c)
		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.QuotaPlatform(c.Request.Context(), key)); err != nil {
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, int64(jev_api.MaxRequestBytes())+1))
		if err != nil || len(body) > jev_api.MaxRequestBytes() {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "request body too large"}})
			return
		}
		model, err := jev_api.ReadModel(body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "unsupported System One model or invalid request"}})
			return
		}
		if model == jev_api.LayaModelID {
			h.relayLaya(c, body)
			return
		}
		if h.contentModerationService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "JEV is not configured"}})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		jev_api.NewHandler(h.contentModerationService).Relay(c)
	})
}

func (h *GatewayHandler) relayLaya(c *gin.Context, body []byte) {
	if h.cfg == nil || !h.cfg.Gateway.Laya.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Laya is not enabled"}})
		return
	}
	status, payload, err := jev_api.RelayLaya(c.Request.Context(), h.cfg.Gateway.Laya.BaseURL(), body, http.DefaultClient)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Laya service unavailable"}})
		return
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		c.JSON(status, gin.H{"error": gin.H{"type": "upstream_error", "message": "Laya returned an error"}})
		return
	}
	c.Data(status, "application/json", payload)
}
