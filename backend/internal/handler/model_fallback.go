package handler

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type modelFallbackAttempt struct {
	Model   string
	Body    []byte
	Mapping service.ChannelMappingResult
}

type modelFallbackState struct {
	candidates []service.ModelFallbackCandidate
	targets    []modelFallbackTarget
	index      int
}

type modelFallbackTarget struct {
	platform      string
	upstreamModel string
}

const modelFallbackStateKey = "model_fallback_state"

// 候选列表固定在本次请求，防止修改档位或别名映射导致循环；有服务端会话状态时不换模型。
func (h *OpenAIGatewayHandler) nextModelFallback(c *gin.Context, apiKey *service.APIKey, model string, body []byte, compact bool) (modelFallbackAttempt, bool) {
	if h.gatewayService == nil {
		return modelFallbackAttempt{}, false
	}
	if reason := modelFallbackReplayBlockReason(c, apiKey, model, body); reason != "" {
		service.RecordOpsModelFallbackBlocked(c, model, reason)
		logger.FromContext(c.Request.Context()).Warn("gateway.model_fallback_blocked",
			zap.String("reason", reason), zap.String("model", model))
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
		routed := state.index <= len(state.targets)
		var target modelFallbackTarget
		if routed {
			target = state.targets[state.index-1]
		}
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
		if value, ok := c.Get(autoRouteObservationKey); ok {
			if route, ok := value.(*service.AutoModelRouteObservation); ok {
				route.AttemptedModels = append(route.AttemptedModels, candidate.Model)
				if observer, ok := c.Writer.(*autoRouteObserverWriter); ok && observer.persist != nil {
					observer.persist()
				}
			}
		}
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
	// 初选保留既有性价比顺序；只有初选失败才按同模型、同系列和版本前缀排序。
	// routes 在进入这里前已完成别名归一、能力预检和账号可用性检查。
	ordered := append([]autoModelRouteCandidate(nil), routes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := calculateAutoModelFallbackSimilarity(selected, ordered[i].model)
		right := calculateAutoModelFallbackSimilarity(selected, ordered[j].model)
		return left.less(right)
	})
	state := &modelFallbackState{}
	skippedSelected := false
	for _, route := range ordered {
		if route.model == selected && !skippedSelected {
			skippedSelected = true
			continue
		}
		state.candidates = append(state.candidates, service.ModelFallbackCandidate{Model: route.model, Tier: "auto"})
		state.targets = append(state.targets, modelFallbackTarget{platform: route.targetPlatform, upstreamModel: route.upstreamModel})
	}
	c.Set(modelFallbackStateKey, state)
}

// 档位虚拟模型先选定档位内首个真实模型，失败后仍使用同一档及更低档的真实候选。
func seedModelTierFallback(c *gin.Context, selected string, routes []autoModelRouteCandidate) {
	if c == nil {
		return
	}
	state := &modelFallbackState{}
	skippedSelected := false
	for _, route := range routes {
		if route.model == selected && !skippedSelected {
			skippedSelected = true
			continue
		}
		state.candidates = append(state.candidates, service.ModelFallbackCandidate{Model: route.model, Tier: "tier"})
		state.targets = append(state.targets, modelFallbackTarget{platform: route.targetPlatform, upstreamModel: route.upstreamModel})
	}
	c.Set(modelFallbackStateKey, state)
}

type autoModelFallbackSimilarity struct {
	exactModel          bool
	sameFamily          bool
	sharedVersionPrefix int
	sharedTokenPrefix   int
	sharedTextPrefix    int
}

func (s autoModelFallbackSimilarity) less(other autoModelFallbackSimilarity) bool {
	if s.exactModel != other.exactModel {
		return s.exactModel
	}
	if s.sameFamily != other.sameFamily {
		return s.sameFamily
	}
	if s.sharedVersionPrefix != other.sharedVersionPrefix {
		return s.sharedVersionPrefix > other.sharedVersionPrefix
	}
	if s.sharedTokenPrefix != other.sharedTokenPrefix {
		return s.sharedTokenPrefix > other.sharedTokenPrefix
	}
	return s.sharedTextPrefix > other.sharedTextPrefix
}

func calculateAutoModelFallbackSimilarity(selected, candidate string) autoModelFallbackSimilarity {
	selectedID := parseAutoModelFallbackID(selected)
	candidateID := parseAutoModelFallbackID(candidate)
	similarity := autoModelFallbackSimilarity{
		exactModel: selectedID.raw == candidateID.raw,
		sameFamily: selectedID.family != "" && selectedID.family == candidateID.family,
	}
	if similarity.sameFamily {
		similarity.sharedVersionPrefix = commonStringPrefix(selectedID.versions, candidateID.versions)
	}
	similarity.sharedTokenPrefix = commonStringPrefix(selectedID.tokens, candidateID.tokens)
	similarity.sharedTextPrefix = commonTextPrefix(selectedID.text, candidateID.text)
	return similarity
}

type autoModelFallbackID struct {
	raw      string
	text     string
	family   string
	tokens   []string
	versions []string
}

func parseAutoModelFallbackID(model string) autoModelFallbackID {
	raw := strings.ToLower(strings.TrimSpace(model))
	text := raw
	if slash := strings.LastIndex(text, "/"); slash >= 0 {
		text = text[slash+1:]
	}
	tokens := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(tokens) == 0 {
		return autoModelFallbackID{raw: raw, text: text}
	}
	first := tokens[0]
	familyEnd := 0
	for familyEnd < len(first) && unicode.IsLetter(rune(first[familyEnd])) {
		familyEnd++
	}
	family := first[:familyEnd]
	versions := make([]string, 0, len(tokens))
	if familyEnd < len(first) {
		versions = append(versions, first[familyEnd:])
	}
	for _, token := range tokens[1:] {
		if token != "" && unicode.IsDigit(rune(token[0])) {
			versions = append(versions, token)
		}
	}
	return autoModelFallbackID{raw: raw, text: text, family: family, tokens: tokens, versions: versions}
}

func commonStringPrefix(left, right []string) int {
	count := 0
	for count < len(left) && count < len(right) && left[count] == right[count] {
		count++
	}
	return count
}

func commonTextPrefix(left, right string) int {
	count := 0
	for count < len(left) && count < len(right) && left[count] == right[count] {
		count++
	}
	return count
}

func modelFallbackReplayableRequest(c *gin.Context, apiKey *service.APIKey, model string, body []byte) bool {
	return modelFallbackReplayBlockReason(c, apiKey, model, body) == ""
}

// 只记录阻断类别，不写入提示词、工具参数或服务端会话标识。
func modelFallbackReplayBlockReason(c *gin.Context, apiKey *service.APIKey, model string, body []byte) string {
	if !openAIRequestAllowsFailoverReplay(c) {
		return "client_disconnected"
	}
	platform := openAICompatibleRequestPlatform(c.Request.Context(), apiKey)
	platformReplayable := platform == service.PlatformOpenAI ||
		(service.IsAutoModelRouting(c.Request.Context()) && autoModelTextPlatform(platform))
	conversation := gjson.GetBytes(body, "conversation")
	switch {
	case !platformReplayable:
		return "unsupported_platform"
	case gjson.GetBytes(body, "previous_response_id").String() != "":
		return "previous_response_id"
	case conversation.Exists() && conversation.Type != gjson.Null:
		return "conversation"
	case service.IsExplicitImageGenerationIntent(c.Request.URL.Path, model, body):
		return "image_generation"
	case !service.ModelFallbackRequestPortable(body):
		return "nonportable_input"
	case fallbackInputHasHostedToolState(gjson.GetBytes(body, "input")) || fallbackInputHasHostedToolState(gjson.GetBytes(body, "messages")):
		return "hosted_tool_state"
	case !fallbackToolsReplayable(gjson.GetBytes(body, "tools")) || !fallbackAdditionalToolsReplayable(gjson.GetBytes(body, "input")):
		return "hosted_tools"
	default:
		return ""
	}
}

// 搜索工具声明不代表已经执行；账号能力预检另行要求原生协议保留工具，真实托管历史仍不可移植。
func fallbackToolsReplayable(tools gjson.Result) bool {
	allowed := true
	tools.ForEach(func(_, tool gjson.Result) bool {
		switch tool.Get("type").String() {
		case "", "function", "custom", "web_search", "web_search_preview", "web_search_preview_2025_03_11":
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

// additional_tools 会合并为有效工具声明，必须与顶层 tools 使用同一重放限制。
func fallbackAdditionalToolsReplayable(input gjson.Result) bool {
	allowed := true
	input.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "additional_tools" {
			allowed = fallbackToolsReplayable(item.Get("tools"))
		}
		return allowed
	})
	return allowed
}

// 只检查输入项与消息内容，不把函数参数或返回值里的同名业务字段当成服务端状态。
func fallbackInputHasHostedToolState(value gjson.Result) bool {
	if value.IsArray() {
		found := false
		value.ForEach(func(_, item gjson.Result) bool {
			found = fallbackInputHasHostedToolState(item)
			return !found
		})
		return found
	}
	if !value.IsObject() {
		return false
	}
	switch value.Get("type").String() {
	case "web_search_call", "file_search_call", "code_interpreter_call", "image_generation_call",
		"mcp_call", "mcp_approval_request", "mcp_approval_response", "mcp_list_tools":
		return true
	case "message", "":
		return fallbackInputHasHostedToolState(value.Get("content"))
	default:
		return false
	}
}

// 选号时可能取得与预筛选不同的账号，因此释放不兼容候选已获得的槽位后继续调度。
func rejectIncompatibleModelFallbackAccount(c *gin.Context, selection *service.AccountSelectionResult, model string, body []byte, requireCompatible bool) bool {
	_, active := c.Get(modelFallbackStateKey)
	if !active && !requireCompatible {
		return false
	}
	var compatible bool
	if c.Request != nil && service.IsAutoModelRouting(c.Request.Context()) {
		compatible = service.AutoModelRequestAccountCompatible(c.Request.Context(), selection.Account, model, body)
	} else {
		compatible = service.ModelFallbackAccountCompatible(selection.Account, model, body)
	}
	if compatible {
		return false
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	return true
}
