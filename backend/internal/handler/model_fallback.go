package handler

import (
	"context"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type modelFallbackAttempt struct {
	Model   string
	Body    []byte
	Mapping service.ChannelMappingResult
}

func nextGPTFallbackModel(model string) string {
	switch model {
	case "gpt-6", "gpt-6-astra":
		return "gpt-5.6-sol"
	case "gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
		return "gpt-5.5"
	default:
		return ""
	}
}

// Called only after account selection or replayable upstream retries exhaust.
// Stateful response IDs cannot safely move to another model.
func (h *OpenAIGatewayHandler) nextGPTFallback(c *gin.Context, apiKey *service.APIKey, model string, body []byte, compact bool) (modelFallbackAttempt, bool) {
	next := nextGPTFallbackModel(model)
	if next == "" || !openAIRequestAllowsFailoverReplay(c) ||
		openAICompatibleRequestPlatform(c.Request.Context(), apiKey) != service.PlatformOpenAI ||
		gjson.GetBytes(body, "previous_response_id").String() != "" ||
		service.IsExplicitImageGenerationIntent(c.Request.URL.Path, model, body) {
		return modelFallbackAttempt{}, false
	}
	mapping, restricted := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, next)
	if restricted {
		return modelFallbackAttempt{}, false
	}
	forwardModel := next
	if mapping.Mapped {
		forwardModel = mapping.MappedModel
	}
	// Preserve the public request for logs while charging the actual fallback.
	original := clientRequestedModel(c, model)
	ctx := context.WithValue(c.Request.Context(), ctxkey.RequestedPublicModel, original)
	ctx = context.WithValue(ctx, ctxkey.ResolvedUpstreamModel, forwardModel)
	ctx = service.WithOpenAIForwardModel(ctx, forwardModel, compact)
	c.Request = c.Request.WithContext(ctx)
	mapping.BillingModelSource = service.BillingModelSourceUpstream
	if !c.Writer.Written() {
		c.Header("X-Sub2api-Requested-Model", original)
		c.Header("X-Sub2api-Fallback-Model", next)
	}
	slog.Warn("openai model fallback", "requested_model", original, "from_model", model, "to_model", next)
	return modelFallbackAttempt{Model: next, Body: h.gatewayService.ReplaceModelInBody(body, forwardModel), Mapping: mapping}, true
}
