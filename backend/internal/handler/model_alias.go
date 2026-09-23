package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) canonicalizeModel(c *gin.Context, model string) (string, error) {
	ctx, policy, err := h.gatewayService.BindModelAliases(c.Request.Context())
	if err != nil {
		requestLogger(c, "gateway.model_alias").Error("gateway.model_alias_unavailable", zap.Error(err))
		return model, err
	}
	canonical := policy.Canonicalize(model)
	if canonical != model {
		if _, ok := service.RequestedPublicModelFromContext(ctx); !ok {
			ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, model)
		}
		c.Set("model_alias_original", model)
		c.Set("model_alias_canonical", canonical)
		c.Header("X-Sub2api-Requested-Model", model)
		c.Header("X-Sub2api-Canonical-Model", canonical)
		requestLogger(c, "gateway.model_alias").Info("gateway.model_alias_resolved", zap.String("requested_model", model), zap.String("canonical_model", canonical))
	}
	c.Request = c.Request.WithContext(ctx)
	// 模型提示词与别名同一次入口绑定，重试/降级共享同一份配置快照。
	if err := h.bindModelSystemPrompts(c); err != nil {
		return canonical, err
	}
	return canonical, nil
}
