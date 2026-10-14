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

// mailProxyEndpoints 是网关允许转发的 edge-mail 路径白名单。
// 保持显式列表，避免把内部/调试端点（如 /internal/context）暴露到公网。
var mailProxyEndpoints = map[string]bool{
	"providers":     true,
	"folders":       true,
	"messages":      true,
	"message":       true,
	"attachment":    true,
	"message/flags": true,
	"send":          true,
}

// MailProxy 转发 edge-mail 的收发件端点，并复用现有 API Key 鉴权、
// 计费资格检查和账号用量记录。邮件凭据只透传给上游，不落盘、不写日志。
func (h *GatewayHandler) MailProxy(c *gin.Context) {
	if h == nil || h.cfg == nil || !h.cfg.Gateway.Mail.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Mail service is not enabled"}})
		return
	}

	endpoint := strings.TrimPrefix(c.Param("proxyPath"), "/")
	// GET /v1/mail/providers 是只读元数据，无需请求体；其余端点都用 POST。
	if endpoint == "providers" {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodPost {
			c.Header("Allow", http.MethodGet+", "+http.MethodPost)
			c.Status(http.StatusMethodNotAllowed)
			return
		}
	} else {
		if !mailProxyEndpoints[endpoint] {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Mail endpoint not found"}})
			return
		}
		if c.Request.Method != http.MethodPost {
			c.Header("Allow", http.MethodPost)
			c.Status(http.StatusMethodNotAllowed)
			return
		}
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

	target, err := url.Parse(h.cfg.Gateway.Mail.BaseURL())
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Mail upstream is not configured"}})
		return
	}
	timeout := time.Duration(h.cfg.Gateway.Mail.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	requestURL := *target
	requestURL.Path = strings.TrimRight(target.Path, "/") + "/" + endpoint
	requestURL.RawQuery = c.Request.URL.RawQuery

	upstreamRequest, err := http.NewRequestWithContext(ctx, c.Request.Method, requestURL.String(), c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Mail request failed"}})
		return
	}
	copyProxyHeaders(c.Request.Header, upstreamRequest.Header)
	upstreamRequest.ContentLength = c.Request.ContentLength

	response, err := http.DefaultClient.Do(upstreamRequest)
	if err != nil {
		logger.L().With(zap.String("component", "handler.mail")).Warn("proxy_error", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Mail service unavailable"}})
		return
	}
	defer response.Body.Close()

	// 附件可能是二进制流，按上限整段读入以便计费后原样回写。
	data, err := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
	if err != nil || len(data) > 32<<20 {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Mail response unavailable"}})
		return
	}

	// 只有成功的实际邮件动作才计费；失败与只读元数据（providers）不计费。
	if response.StatusCode >= 200 && response.StatusCode < 300 && endpoint != "providers" {
		if err := h.recordMailUsage(c, apiKey, subscription, quotaPlatform, endpoint, requestURL.Path); err != nil {
			logger.L().With(zap.String("component", "handler.mail")).Error("billing_failed", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "billing_error", "message": "Mail billing unavailable"}})
			return
		}
	}

	copyResponseHeaders(response.Header, c.Writer.Header())
	c.Data(response.StatusCode, response.Header.Get("Content-Type"), data)
}

func (h *GatewayHandler) recordMailUsage(
	c *gin.Context,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	quotaPlatform string,
	endpoint string,
	upstreamPath string,
) error {
	selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, "", "", nil, "", apiKey.UserID)
	if err != nil || selection == nil || selection.Account == nil {
		return err
	}
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	if !selection.Acquired && selection.ReleaseFunc == nil {
		return service.ErrNoAvailableAccounts
	}
	requestID := "mail:" + uuid.NewString()
	// 与 vision/media 一致：按次计费，具体单价由分组价格覆盖。
	model := "edge-mail-" + strings.ReplaceAll(endpoint, "/", "-")
	return h.gatewayService.RecordUsage(c.Request.Context(), &service.RecordUsageInput{
		Result: &service.ForwardResult{RequestID: requestID, Model: model, VisionCount: 1},
		APIKey: apiKey, User: apiKey.User, Account: selection.Account,
		Subscription: subscription, InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: upstreamPath,
		UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
		APIKeyService: h.apiKeyService, QuotaPlatform: quotaPlatform,
		RequestPayloadHash: service.HashUsageRequestPayload([]byte(requestID)),
	})
}
