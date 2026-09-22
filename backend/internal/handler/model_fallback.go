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

type modelFallbackState struct {
	candidates []service.ModelFallbackCandidate
	index      int
}

const modelFallbackStateKey = "model_fallback_state"

// 候选列表固定在本次请求，防止修改档位或别名映射导致循环；有服务端会话状态时不换模型。
func (h *OpenAIGatewayHandler) nextModelFallback(c *gin.Context, apiKey *service.APIKey, model string, body []byte, compact bool) (modelFallbackAttempt, bool) {
	if !openAIRequestAllowsFailoverReplay(c) || h.gatewayService == nil ||
		openAICompatibleRequestPlatform(c.Request.Context(), apiKey) != service.PlatformOpenAI ||
		gjson.GetBytes(body, "previous_response_id").String() != "" ||
		gjson.GetBytes(body, "conversation").Exists() ||
		service.IsExplicitImageGenerationIntent(c.Request.URL.Path, model, body) || !fallbackToolsReplayable(gjson.GetBytes(body, "tools")) {
		return modelFallbackAttempt{}, false
	}
	value, found := c.Get(modelFallbackStateKey)
	state, _ := value.(*modelFallbackState)
	if !found {
		candidates, err := h.gatewayService.ModelFallbackCandidates(c.Request.Context(), apiKey.GroupID, model, body)
		state = &modelFallbackState{candidates: candidates}
		c.Set(modelFallbackStateKey, state)
		if err != nil {
			slog.Warn("model fallback policy unavailable", "error", err)
			return modelFallbackAttempt{}, false
		}
	}
	if state == nil {
		return modelFallbackAttempt{}, false
	}
	for state.index < len(state.candidates) {
		candidate := state.candidates[state.index]
		state.index++
		mapping, restricted := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, candidate.Model)
		if restricted {
			continue
		}
		forwardModel := candidate.Model
		if mapping.Mapped {
			forwardModel = mapping.MappedModel
		}
		original := clientRequestedModel(c, model)
		ctx := context.WithValue(c.Request.Context(), ctxkey.RequestedPublicModel, original)
		ctx = context.WithValue(ctx, ctxkey.ResolvedUpstreamModel, forwardModel)
		ctx = service.WithOpenAIForwardModel(ctx, forwardModel, compact)
		c.Request = c.Request.WithContext(ctx)
		mapping.BillingModelSource = service.BillingModelSourceUpstream
		if !c.Writer.Written() {
			c.Header("X-Sub2api-Requested-Model", original)
			c.Header("X-Sub2api-Fallback-Model", candidate.Model)
		}
		service.RecordOpsModelFallback(c, model, candidate.Model, candidate.Tier)
		slog.Warn("openai model fallback", "requested_model", original, "from_model", model, "to_model", candidate.Model, "tier", candidate.Tier)
		return modelFallbackAttempt{Model: candidate.Model, Body: h.gatewayService.ReplaceModelInBody(body, forwardModel), Mapping: mapping}, true
	}
	return modelFallbackAttempt{}, false
}

// 服务端托管工具的执行状态不能在模型间移植；普通函数工具保留完整输入并允许重放。
func fallbackToolsReplayable(tools gjson.Result) bool {
	allowed := true
	tools.ForEach(func(_, tool gjson.Result) bool {
		switch tool.Get("type").String() {
		case "", "function":
			allowed = true
		case "namespace":
			allowed = fallbackToolsReplayable(tool.Get("tools"))
		default:
			allowed = false
		}
		return allowed
	})
	return allowed
}

// 选号时可能取得与预筛选不同的账号，因此释放不兼容候选已获得的槽位后继续调度。
func rejectIncompatibleModelFallbackAccount(c *gin.Context, selection *service.AccountSelectionResult, model string, body []byte) bool {
	if _, active := c.Get(modelFallbackStateKey); !active || service.ModelFallbackAccountCompatible(selection.Account, model, body) {
		return false
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	return true
}
