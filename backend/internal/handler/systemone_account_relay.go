package handler

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/jev_api"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// systemOnePlatformForModel 把客户端 model 映射到负责它的平台。
//
// System One 的 model 名本身就是平台选择器（与 /v1/systemone 的既有约定一致）：
// `laya*` 走本地 Laya 决策模型，`typesafe/jev` 走 JEV。两者共用 wire protocol，
// 只是上游账号不同。
func systemOnePlatformForModel(model string) string {
	if strings.HasPrefix(model, jev_api.LayaModelID) {
		return service.PlatformLaya
	}
	return service.PlatformJev
}

// systemOneSchedulingContext 为调度器指定模型所属平台。
func systemOneSchedulingContext(ctx context.Context, platform string) context.Context {
	return context.WithValue(ctx, ctxkey.ForcePlatform, platform)
}

// RegisterSystemOneAccountRelay 把 System One 挂到账号池上。
//
// 与旧的 relayLaya（读全局 gateway.laya.url）不同：这里按 model 选定平台后，
// 复用与 /vision 一致的「调度选号 → 转发 → 记用量 → 关联真实账号」流程，
// 使 laya/jev 与其它平台在账号、分组、计费上完全对齐。
func (h *GatewayHandler) RegisterSystemOneAccountRelay(group *gin.RouterGroup) {
	if h == nil {
		return
	}
	group.POST("/systemone", h.SystemOneRelay)
}

// SystemOneRelay 处理 POST /v1/systemone。
func (h *GatewayHandler) SystemOneRelay(c *gin.Context) {
	if h == nil || h.gatewayService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "System One relay is not available"}})
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.GroupID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "API key required"}})
		return
	}
	if h.billingCacheService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Billing eligibility unavailable"}})
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
	platform := systemOnePlatformForModel(model)

	// 按模型指定平台，避免混合分组默认平台把请求调度到其它账号。
	selectionCtx := systemOneSchedulingContext(c.Request.Context(), platform)
	selection, err := h.gatewayService.SelectAccountWithLoadAwareness(
		selectionCtx, apiKey.GroupID, "", model, nil, "", apiKey.UserID,
	)
	if err != nil || selection == nil || selection.Account == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"type": "scheduling_error", "message": "No available account for System One model " + model},
		})
		return
	}
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	if !selection.Acquired && selection.ReleaseFunc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "System One account is busy"}})
		return
	}
	account := selection.Account
	if account.Platform != platform {
		// 选到的账号平台与 model 要求的平台不符时直接拒绝，避免把 JEV 请求打到 Laya 账号。
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"type": "scheduling_error", "message": "No available " + platform + " account for System One model " + model},
		})
		return
	}

	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = systemOneAccountBaseURL(account)
	}
	if baseURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"type": "service_unavailable", "message": "System One account has no upstream address"},
		})
		return
	}
	apiKeyValue, _ := account.Credentials["api_key"].(string)

	status, payload, err := jev_api.RelaySystemOne(c.Request.Context(), baseURL, apiKeyValue, body, http.DefaultClient)
	if err != nil {
		logger.L().With(zap.String("component", "handler.systemone")).Warn("relay_error",
			zap.Int64("account_id", account.ID), zap.String("platform", platform), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "System One service unavailable"}})
		return
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		c.JSON(status, gin.H{"error": gin.H{"type": "upstream_error", "message": "System One upstream returned an error"}})
		return
	}

	// 决策模型不生成 token，也没有可用的按次单价；这里只记录调用与真实账号关联，
	// 不写入任何计费量（沿用仓库既有约定：未定义定价前不宣称已计费）。
	requestID := "systemone:" + uuid.NewString()
	input := &service.RecordUsageInput{
		Result: &service.ForwardResult{
			RequestID: requestID,
			Model:     model,
		},
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            account,
		Subscription:       subscription,
		InboundEndpoint:    GetInboundEndpoint(c),
		UpstreamEndpoint:   "/v1/systemone",
		UserAgent:          c.GetHeader("User-Agent"),
		IPAddress:          ip.GetClientIP(c),
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      quotaPlatform,
		RequestPayloadHash: service.HashUsageRequestPayload(body),
	}
	if err := h.gatewayService.RecordUsage(c.Request.Context(), input); err != nil {
		logger.L().With(zap.String("component", "handler.systemone")).Error("billing_failed", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "billing_error", "message": "System One billing unavailable"}})
		return
	}
	c.Data(status, "application/json", payload)
}

// systemOneAccountBaseURL 兜底给出平台的默认上游地址（账号未显式配置 base_url 时）。
// 与其它内置适配器一致：地址由后端按平台注入，账号上不需要手填。
func systemOneAccountBaseURL(account *service.Account) string {
	if account == nil {
		return ""
	}
	return service.BuiltinAdapterBaseURLForPlatform(account.Platform)
}
