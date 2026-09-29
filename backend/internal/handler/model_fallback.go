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
	targets    map[string]modelFallbackTarget
	index      int
}

type modelFallbackTarget struct {
	platform      string
	upstreamModel string
}

const modelFallbackStateKey = "model_fallback_state"

// 候选列表固定在本次请求，防止修改档位或别名映射导致循环；有服务端会话状态时不换模型。
func (h *OpenAIGatewayHandler) nextModelFallback(c *gin.Context, apiKey *service.APIKey, model string, body []byte, compact bool) (modelFallbackAttempt, bool) {
	if h.gatewayService == nil || !modelFallbackReplayableRequest(c, apiKey, model, body) {
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
		targetModel := candidate.Model
		target, routed := state.targets[candidate.Model]
		if routed && target.upstreamModel != "" {
			targetModel = target.upstreamModel
		}
		mapping, restricted := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, targetModel)
		if restricted {
			continue
		}
		forwardModel := targetModel
		if mapping.Mapped {
			forwardModel = mapping.MappedModel
		}
		if !service.AutoModelAllowed(c.Request.Context(), candidate.Model, targetModel, forwardModel) {
			continue
		}
		original := clientRequestedModel(c, model)
		if routed {
			original = candidate.Model
		}
		ctx := context.WithValue(c.Request.Context(), ctxkey.RequestedPublicModel, original)
		ctx = context.WithValue(ctx, ctxkey.ResolvedUpstreamModel, forwardModel)
		if routed && target.platform != "" {
			ctx = service.WithResolvedTargetPlatform(ctx, target.platform)
		}
		ctx = service.WithOpenAIForwardModel(ctx, forwardModel, compact)
		c.Request = c.Request.WithContext(ctx)
		mapping.BillingModelSource = service.BillingModelSourceUpstream
		if !c.Writer.Written() {
			c.Header("X-Sub2api-Requested-Model", original)
			c.Header("X-Sub2api-Fallback-Model", candidate.Model)
		}
		service.RecordOpsModelFallback(c, model, candidate.Model, candidate.Tier)
		slog.Warn("openai model fallback", "requested_model", original, "from_model", model, "to_model", candidate.Model, "tier", candidate.Tier)
		return modelFallbackAttempt{Model: targetModel, Body: h.gatewayService.ReplaceModelInBody(body, forwardModel), Mapping: mapping}, true
	}
	return modelFallbackAttempt{}, false
}

// Auto 候选固定在当前请求内，失败时只会切换到已经通过能力预检的模型。
func seedAutoModelFallback(c *gin.Context, selected string, routes []autoModelRouteCandidate) {
	if c == nil {
		return
	}
	state := &modelFallbackState{targets: make(map[string]modelFallbackTarget, len(routes))}
	for _, route := range routes {
		if route.model == selected {
			continue
		}
		state.candidates = append(state.candidates, service.ModelFallbackCandidate{Model: route.model, Tier: "auto"})
		state.targets[route.model] = modelFallbackTarget{platform: route.targetPlatform, upstreamModel: route.upstreamModel}
	}
	c.Set(modelFallbackStateKey, state)
}

func modelFallbackReplayableRequest(c *gin.Context, apiKey *service.APIKey, model string, body []byte) bool {
	if !openAIRequestAllowsFailoverReplay(c) ||
		openAICompatibleRequestPlatform(c.Request.Context(), apiKey) != service.PlatformOpenAI ||
		gjson.GetBytes(body, "previous_response_id").String() != "" ||
		gjson.GetBytes(body, "conversation").Exists() ||
		service.IsExplicitImageGenerationIntent(c.Request.URL.Path, model, body) ||
		!service.ModelFallbackRequestPortable(body) || !fallbackToolsReplayable(gjson.GetBytes(body, "tools")) {
		return false
	}
	return true
}

// 服务端托管工具的执行状态不能在模型间移植；普通函数工具保留完整输入并允许重放。
func fallbackToolsReplayable(tools gjson.Result) bool {
	allowed := true
	tools.ForEach(func(_, tool gjson.Result) bool {
		switch tool.Get("type").String() {
		case "", "function", "custom":
			allowed = true
		case "namespace":
			allowed = fallbackToolsReplayable(tool.Get("tools"))
		case "tool_search":
			allowed = tool.Get("execution").String() == "client"
		default:
			allowed = false
		}
		return allowed
	})
	return allowed
}

// 选号时可能取得与预筛选不同的账号，因此释放不兼容候选已获得的槽位后继续调度。
func rejectIncompatibleModelFallbackAccount(c *gin.Context, selection *service.AccountSelectionResult, model string, body []byte, requireCompatible bool) bool {
	_, active := c.Get(modelFallbackStateKey)
	if (!active && !requireCompatible) || service.ModelFallbackAccountCompatible(selection.Account, model, body) {
		return false
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	return true
}
