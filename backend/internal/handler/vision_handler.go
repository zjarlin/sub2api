package handler

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// VisionProxy 仅转发已声明的离线视觉推理端点，并在成功后关联真实分组账号计费。
func (h *GatewayHandler) VisionProxy(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Vision service not available"}})
		return
	}
	if !h.cfg.Gateway.Vision.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Vision service is not enabled"}})
		return
	}
	endpoint := strings.TrimPrefix(c.Param("proxyPath"), "/")
	switch endpoint {
	case "detect", "segment", "pose", "classify", "ocr":
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Vision endpoint not found"}})
		return
	}
	if c.Request.Method != http.MethodPost {
		c.Header("Allow", http.MethodPost)
		c.Status(http.StatusMethodNotAllowed)
		return
	}

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.GroupID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "API key required"}})
		return
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	// 复用现有计费资格检查（余额/订阅/额度）。
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, quotaPlatform); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
		return
	}

	target, err := url.Parse(h.cfg.Gateway.Vision.BaseURL())
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Vision upstream is not configured"}})
		return
	}
	timeout := time.Duration(h.cfg.Gateway.Vision.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	requestURL := *target
	requestURL.Path = strings.TrimRight(target.Path, "/") + "/" + endpoint
	requestURL.RawQuery = ""
	upstreamRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Vision request failed"}})
		return
	}
	upstreamRequest.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	upstreamRequest.ContentLength = c.Request.ContentLength
	response, err := http.DefaultClient.Do(upstreamRequest)
	if err != nil {
		logger.L().With(zap.String("component", "handler.vision")).Warn("proxy_error", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Vision service unavailable"}})
		return
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Vision response unavailable"}})
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		c.Data(response.StatusCode, response.Header.Get("Content-Type"), data)
		return
	}
	selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, "", "", nil, "", apiKey.UserID)
	if err != nil || selection == nil || selection.Account == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "No available account for vision usage"}})
		return
	}
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	if !selection.Acquired && selection.ReleaseFunc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Vision account is busy"}})
		return
	}
	requestID := "vision:" + uuid.NewString()
	input := &service.RecordUsageInput{
		Result: &service.ForwardResult{RequestID: requestID, Model: "edge-vision-" + endpoint, VisionCount: 1},
		APIKey: apiKey, User: apiKey.User, Account: selection.Account, Subscription: subscription,
		InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: requestURL.Path,
		UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
		APIKeyService: h.apiKeyService, QuotaPlatform: quotaPlatform,
		RequestPayloadHash: service.HashUsageRequestPayload([]byte(requestID)),
	}
	if err := h.gatewayService.RecordUsage(c.Request.Context(), input); err != nil {
		logger.L().With(zap.String("component", "handler.vision")).Error("billing_failed", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "billing_error", "message": "Vision billing unavailable"}})
		return
	}
	c.Data(http.StatusOK, "application/json", data)
}
