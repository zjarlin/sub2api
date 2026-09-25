package handler

import (
	"bytes"
	"context"
	"encoding/json"
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

// MediaProxy 转发 edge-media 的配音与视频任务端点，并复用现有 API Key
// 鉴权、计费资格检查和账号用量记录。TTS 请求体缓冲后解析文本长度用于
// 计费，其余端点（可能携带大视频）直接流式转发，响应也直接流回客户端。
func (h *GatewayHandler) MediaProxy(c *gin.Context) {
	if h == nil || h.cfg == nil || !h.cfg.Gateway.Media.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Media service is not enabled"}})
		return
	}

	endpoint := strings.TrimPrefix(c.Param("proxyPath"), "/")
	switch endpoint {
	case "tts", "v1/audio/speech", "videos/dub", "videos/generations", "videos/transcode":
	default:
		if c.Request.Method == http.MethodGet && strings.HasPrefix(endpoint, "tasks/") {
			break
		}
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Media endpoint not found"}})
		return
	}
	if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodGet {
		c.Header("Allow", http.MethodPost+", "+http.MethodGet)
		c.Status(http.StatusMethodNotAllowed)
		return
	}

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.GroupID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "API key required"}})
		return
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, quotaPlatform); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
		return
	}

	target, err := url.Parse(h.cfg.Gateway.Media.BaseURL())
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Media upstream is not configured"}})
		return
	}
	timeout := time.Duration(h.cfg.Gateway.Media.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 3600 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	requestURL := *target
	requestURL.Path = strings.TrimRight(target.Path, "/") + "/" + endpoint
	requestURL.RawQuery = c.Request.URL.RawQuery
	// 只有 TTS 需要按文本长度计费，必须缓冲请求体解析字符数；其余端点可能
	// 携带 512MB 级视频，直接流式转发，避免整段读入内存。
	needsTextBilling := isTTSEndpoint(endpoint)
	var requestBody []byte
	var bodyReader io.Reader = c.Request.Body
	if needsTextBilling {
		requestBody, err = io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Media request body unavailable"}})
			return
		}
		bodyReader = bytes.NewReader(requestBody)
	}
	upstreamRequest, err := http.NewRequestWithContext(ctx, c.Request.Method, requestURL.String(), bodyReader)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Media request failed"}})
		return
	}
	copyProxyHeaders(c.Request.Header, upstreamRequest.Header)
	if needsTextBilling {
		upstreamRequest.ContentLength = int64(len(requestBody))
	} else {
		upstreamRequest.ContentLength = c.Request.ContentLength
	}

	response, err := http.DefaultClient.Do(upstreamRequest)
	if err != nil {
		logger.L().With(zap.String("component", "handler.media")).Warn("proxy_error", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Media service unavailable"}})
		return
	}
	defer response.Body.Close()

	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := h.recordMediaUsage(c, apiKey, subscription, quotaPlatform, endpoint, requestURL.Path, c.GetHeader("Content-Type"), requestBody); err != nil {
			logger.L().With(zap.String("component", "handler.media")).Error("billing_failed", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "billing_error", "message": "Media billing unavailable"}})
			return
		}
	}
	copyResponseHeaders(response.Header, c.Writer.Header())
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func copyProxyHeaders(src http.Header, dst http.Header) {
	for _, name := range []string{"Content-Type", "Accept", "User-Agent"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}

func copyResponseHeaders(src http.Header, dst http.Header) {
	for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Length"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}

func (h *GatewayHandler) recordMediaUsage(
	c *gin.Context,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	quotaPlatform string,
	endpoint string,
	upstreamPath string,
	contentType string,
	requestBody []byte,
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
	requestID := "media:" + uuid.NewString()
	model := "edge-media-" + strings.ReplaceAll(endpoint, "/", "-")
	result := &service.ForwardResult{RequestID: requestID, Model: model}
	if isTTSEndpoint(endpoint) {
		chars := mediaTextCharacters(contentType, requestBody)
		if chars > 0 {
			result.AudioUsage = &service.AudioUsage{Mode: "tts", DurationOrUnits: float64(chars) / 1_000_000.0}
		} else {
			// 文本长度不可解析时不能按 0 计费，否则会形成免费旁路。
			result.VisionCount = 1
		}
	} else {
		// 视频任务按次计费时沿用视觉服务的按次口径，再由分组价格覆盖。
		result.VisionCount = 1
	}
	return h.gatewayService.RecordUsage(c.Request.Context(), &service.RecordUsageInput{
		Result: result, APIKey: apiKey, User: apiKey.User, Account: selection.Account,
		Subscription: subscription, InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: upstreamPath,
		UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
		APIKeyService: h.apiKeyService, QuotaPlatform: quotaPlatform,
		RequestPayloadHash: service.HashUsageRequestPayload([]byte(requestID)),
	})
}

func mediaTextCharacters(contentType string, body []byte) int {
	if len(body) == 0 {
		return 0
	}
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			for _, key := range []string{"text", "input", "prompt"} {
				if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
					return len([]rune(value))
				}
			}
		}
	}
	return 0
}

// isTTSEndpoint 标记需要按文本长度计费的端点。
func isTTSEndpoint(endpoint string) bool {
	return endpoint == "tts" || endpoint == "v1/audio/speech"
}
