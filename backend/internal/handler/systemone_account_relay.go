package handler

import (
	"context"
	"errors"
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

// selectSystemOneAccount 按模型所属平台调度账号，并拒绝跨平台命中。
//
// 返回的选择结果由调用方负责释放；platform 是模型要求的真实平台，
// 不能沿用分组默认平台，否则混合分组可能把 JEV 请求打到 Laya 账号。
func (h *GatewayHandler) selectSystemOneAccount(
	ctx context.Context, groupID *int64, model, platform string, sub2apiUserID int64,
) (*service.AccountSelectionResult, error) {
	selectionCtx := systemOneSchedulingContext(ctx, platform)
	selection, err := h.gatewayService.SelectAccountWithLoadAwareness(
		selectionCtx, groupID, "", model, nil, "", sub2apiUserID,
	)
	if err != nil {
		return nil, err
	}
	if selection == nil || selection.Account == nil {
		return nil, service.ErrNoAvailableAccounts
	}
	if selection.Account.Platform != platform {
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return nil, service.ErrNoAvailableAccounts
	}
	return selection, nil
}

// systemOneFallbackPlatform 报告请求平台失败时是否应隐式回退，以及回退到哪个平台。
// 只有 JEV 会回退到 Laya；Laya 永远不会反向回退，避免语义反转与无限回退。
func systemOneFallbackPlatform(requestedPlatform string) (string, bool) {
	if requestedPlatform == service.PlatformJev {
		return service.PlatformLaya, true
	}
	return "", false
}

// systemOneRelayFailureShouldFallback 判断一次上游失败是否值得改用 Laya。
// 只有可用性故障（网络错误、超时、5xx/429/408）才回退；4xx 请求错误直接透传，
// 避免把「请求体不合法」误判成「JEV 不可用」并掩盖真实原因。
func systemOneRelayFailureShouldFallback(requestedPlatform string, status int, relayErr error) bool {
	if requestedPlatform != service.PlatformJev {
		return false
	}
	if relayErr != nil {
		return true
	}
	switch {
	case status == http.StatusTooManyRequests,
		status == http.StatusRequestTimeout,
		status == http.StatusBadGateway,
		status == http.StatusServiceUnavailable,
		status == http.StatusGatewayTimeout:
		return true
	case status >= http.StatusInternalServerError:
		return true
	default:
		return false
	}
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
	requestedPlatform := platform
	fallbackUsed := false

	selection, err := h.selectSystemOneAccount(c.Request.Context(), apiKey.GroupID, model, requestedPlatform, apiKey.UserID)
	if err != nil || selection == nil || selection.Account == nil {
		fallbackPlatform, canFallback := systemOneFallbackPlatform(requestedPlatform)
		if !canFallback {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{"type": "scheduling_error", "message": "No available " + requestedPlatform + " account for System One model " + model},
			})
			return
		}
		// JEV 选号失败：隐式回退 Laya，客户端无感。
		selection, err = h.selectSystemOneAccount(
			c.Request.Context(), apiKey.GroupID, jev_api.LayaModelID, fallbackPlatform, apiKey.UserID,
		)
		if err != nil || selection == nil || selection.Account == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{"type": "scheduling_error", "message": "No available System One account for model " + model + " or fallback " + fallbackPlatform},
			})
			return
		}
		platform = fallbackPlatform
		fallbackUsed = true
		// 回退后用量归属实际服务平台，避免把 Laya 用量记到 JEV 名下。
		quotaPlatform = fallbackPlatform
	} else {
		platform = requestedPlatform
	}
	var releaseSelection = func() {}
	if selection.ReleaseFunc != nil {
		release := selection.ReleaseFunc
		var released bool
		releaseSelection = func() {
			if released {
				return
			}
			released = true
			release()
		}
		defer func() { releaseSelection() }()
	}
	if !selection.Acquired && selection.ReleaseFunc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "System One account is busy"}})
		return
	}
	account := selection.Account
	relay := func(selection *service.AccountSelectionResult, selectedPlatform string) (int, []byte, error, error) {
		account := selection.Account
		upstreamModel := model
		if selectedPlatform == service.PlatformLaya {
			// Laya 的公开模型名可能带检查点后缀，回退时统一使用自动选检查点的 laya。
			upstreamModel = jev_api.LayaModelID
		}
		baseURL := account.GetOpenAIBaseURL()
		if baseURL == "" {
			baseURL = systemOneAccountBaseURL(account)
		}
		if baseURL == "" {
			return 0, nil, nil, errors.New("System One account has no upstream address")
		}
		apiKeyValue, _ := account.Credentials["api_key"].(string)
		relayBody := body
		if upstreamModel != model {
			var rewriteErr error
			relayBody, rewriteErr = jev_api.RewriteModel(body, upstreamModel)
			if rewriteErr != nil {
				return 0, nil, nil, rewriteErr
			}
		}
		status, payload, err := jev_api.RelaySystemOne(c.Request.Context(), baseURL, apiKeyValue, relayBody, http.DefaultClient)
		return status, payload, err, nil
	}

	status, payload, relayErr, setupErr := relay(selection, platform)
	if setupErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"type": "service_unavailable", "message": setupErr.Error()},
		})
		return
	}
	upstreamFailed := relayErr != nil || status < http.StatusOK || status >= http.StatusMultipleChoices
	// 只有 JEV 请求才回退，且必须是因为可用性故障；4xx 属于请求问题，回退会掩盖真实错误。
	if upstreamFailed && !fallbackUsed && systemOneRelayFailureShouldFallback(requestedPlatform, status, relayErr) {
		logger.L().With(zap.String("component", "handler.systemone")).Warn("systemone_relay_failed_fallback_laya",
			zap.Int64("account_id", account.ID), zap.Int("upstream_status", status), zap.Error(relayErr))
		// 释放 JEV 槽位后再尝试 Laya；releaseSelection 保证不会重复释放。
		releaseSelection()
		fallback, fallbackErr := h.selectSystemOneAccount(c.Request.Context(), apiKey.GroupID, jev_api.LayaModelID, service.PlatformLaya, apiKey.UserID)
		if fallbackErr == nil && fallback != nil && fallback.Account != nil {
			if fallback.ReleaseFunc != nil {
				fallbackRelease := fallback.ReleaseFunc
				var fallbackReleased bool
				releaseSelection = func() {
					if fallbackReleased {
						return
					}
					fallbackReleased = true
					fallbackRelease()
				}
			}
			if !fallback.Acquired && fallback.ReleaseFunc == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "System One account is busy"}})
				return
			}
			selection = fallback
			platform = service.PlatformLaya
			fallbackUsed = true
			// 回退后用量归属实际服务平台，避免把 Laya 用量记到 JEV 名下。
			quotaPlatform = service.PlatformLaya
			status, payload, relayErr, setupErr = relay(fallback, platform)
			if setupErr != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{"type": "service_unavailable", "message": setupErr.Error()},
				})
				return
			}
			upstreamFailed = relayErr != nil || status < http.StatusOK || status >= http.StatusMultipleChoices
		}
	}
	if upstreamFailed {
		logger.L().With(zap.String("component", "handler.systemone")).Warn("relay_failed",
			zap.Int64("account_id", selection.Account.ID), zap.String("platform", platform),
			zap.Int("upstream_status", status), zap.Bool("fallback_used", fallbackUsed), zap.Error(relayErr))
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			c.JSON(status, gin.H{"error": gin.H{"type": "upstream_error", "message": "System One upstream returned an error"}})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "System One service unavailable"}})
		return
	}
	account = selection.Account

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
