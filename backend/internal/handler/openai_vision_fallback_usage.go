package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 辅助用量在每次转发返回后取出，主模型失败、换号或客户端断开也不能漏账。
func (h *OpenAIGatewayHandler) recordVisionFallbackUsage(c *gin.Context, apiKey *service.APIKey, subscription *service.UserSubscription) {
	for _, usage := range service.TakeVisionFallbackUsage(c) {
		input := &service.OpenAIRecordUsageInput{
			Result: usage.Result, APIKey: apiKey, User: apiKey.User, Account: usage.Account,
			Subscription: subscription, APIKeyService: h.apiKeyService,
			InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: usage.Result.UpstreamEndpoint,
			UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
			SessionID: service.ExtractClientSessionID(c), RequestPayloadHash: usage.PayloadHash,
			QuotaPlatform: usage.Account.Platform, PricingAt: usage.PricingAt,
		}
		h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, input); err != nil {
				logger.LegacyPrintf("handler.vision_fallback", "记录视觉辅助用量失败: account_id=%d err=%v", input.Account.ID, err)
			}
		})
	}
}
