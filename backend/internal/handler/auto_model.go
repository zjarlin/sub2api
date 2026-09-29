package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/jev_api"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const (
	autoModelID             = "auto"
	autoModelStateByteLimit = 16 << 10
	autoModelListingKey     = "auto_model_listing"
	autoModelListingETagKey = "auto_model_listing_etag"
)

var errAutoModelUsageRecord = errors.New("auto model decision usage record failed")
var errAutoModelDecisionRejected = errors.New("System One rejected the decision request")

type autoModelRouteCandidate struct {
	model          string
	targetPlatform string
	upstreamModel  string
}

func prependAutoModel(models []string) []string {
	withAuto := make([]string, 0, len(models)+1)
	withAuto = append(withAuto, autoModelID)
	for _, model := range models {
		if model != autoModelID {
			withAuto = append(withAuto, model)
		}
	}
	return withAuto
}

// autoModelCandidates 只使用分组目录里可识别的文本模型；决策模型与媒体模型不能作为生成目标。
func autoModelCandidates(models []string) []string {
	candidates := make([]string, 0, len(models))
	for _, model := range service.FilterCodexModelIDsForGroup(models, nil) {
		if model == autoModelID || !autoModelTextCandidate(model) {
			continue
		}
		candidates = append(candidates, model)
	}
	return candidates
}

func autoModelCandidatesForGroup(group *service.Group, models []string) []string {
	candidates := autoModelCandidates(models)
	if group == nil || group.Platform != service.PlatformOpenAI {
		return candidates
	}
	filtered := make([]string, 0, len(candidates))
	for _, model := range candidates {
		platform, _ := service.DetectModelPlatform(model)
		if platform == service.PlatformOpenAI {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func (h *GatewayHandler) autoModelTargetAllowed(ctx context.Context, groupID int64, model string) bool {
	if !service.AutoModelAllowed(ctx, model) {
		return false
	}
	mapping := h.gatewayService.ResolveChannelMapping(ctx, groupID, model)
	return !mapping.Mapped || service.AutoModelAllowed(ctx, mapping.MappedModel)
}

// 最高档排除在决策前生效，渠道改名也不能绕过本轮成本限制。
func (h *GatewayHandler) autoModelEligibleCandidates(ctx context.Context, group *service.Group, models []string) []string {
	candidates := service.ModelAliasesFromContext(ctx).CanonicalIDs(autoModelCandidatesForGroup(group, models))
	filtered := make([]string, 0, len(candidates))
	for _, model := range candidates {
		if h.autoModelTargetAllowed(ctx, group.ID, model) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func autoModelTextCandidate(model string) bool {
	platform, ok := service.DetectModelPlatform(model)
	if !ok || !autoModelTextPlatform(platform) {
		return false
	}
	name := strings.ToLower(model)
	for _, specialized := range []string{
		"embedding", "moderation", "image", "audio", "video", "tts-", "whisper", "transcri", "dall-e",
		"translate", "translation", "safety", "guard", "calibration", "rerank", "re-rank", "classifier", "reward-model", "parser",
	} {
		if strings.Contains(name, specialized) {
			return false
		}
	}
	return true
}

// 候选必须同时具备文本路由和本次请求所需的账号能力。
func (h *GatewayHandler) autoModelRoutableCandidates(
	ctx context.Context,
	group *service.Group,
	resolver *service.CompositeRouteResolver,
	path string,
	body []byte,
	candidates []string,
) ([]autoModelRouteCandidate, error) {
	routes := make([]autoModelRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		route := autoModelRouteCandidate{model: candidate, targetPlatform: service.PlatformOpenAI, upstreamModel: candidate}
		if group.Platform == service.PlatformComposite {
			decision, err := resolver.Resolve(ctx, group.ID, candidate, autoModelEndpoint(path))
			if err != nil {
				return nil, err
			}
			if !decision.Matched || !autoModelTextPlatform(decision.TargetPlatform) ||
				!h.autoModelTargetAllowed(ctx, group.ID, decision.UpstreamModel) {
				continue
			}
			route.targetPlatform = decision.TargetPlatform
			route.upstreamModel = decision.UpstreamModel
		}
		compatible, err := h.gatewayService.AutoModelAccountCompatible(
			ctx,
			&group.ID,
			route.targetPlatform,
			route.upstreamModel,
			body,
		)
		if err != nil {
			return nil, err
		}
		if compatible {
			routes = append(routes, route)
		}
	}
	return routes, nil
}

func autoModelTextPlatform(platform string) bool {
	switch platform {
	case service.PlatformOpenAI, service.PlatformGrok, service.PlatformKimi,
		service.PlatformZhipu, service.PlatformDeepseek, service.PlatformMiniMax,
		service.PlatformOpenCodeGo, service.PlatformDoubao, service.PlatformTraework,
		service.PlatformWorkbuddy, service.PlatformVibex, service.PlatformZcode,
		service.PlatformQoder:
		return true
	default:
		return false
	}
}

func (h *GatewayHandler) autoModelAvailable(ctx context.Context, group *service.Group, models []string) bool {
	if h == nil || h.gatewayService == nil || group == nil ||
		(group.Platform != service.PlatformComposite && group.Platform != service.PlatformOpenAI) ||
		(group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(autoModelID)) {
		return false
	}
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(ctx)
	if err != nil || len(h.autoModelEligibleCandidates(ctx, group, models)) == 0 {
		return false
	}
	platforms := h.gatewayService.GetSchedulablePlatforms(ctx, &group.ID)
	if group.Platform == service.PlatformOpenAI {
		if _, ok := platforms[service.PlatformOpenAI]; !ok {
			return false
		}
	}
	_, laya := platforms[service.PlatformLaya]
	_, jev := platforms[service.PlatformJev]
	return laya || jev
}

func (h *GatewayHandler) autoModelCatalog(ctx context.Context, group *service.Group) []string {
	if h == nil || h.gatewayService == nil || group == nil {
		return nil
	}
	if group.Platform == service.PlatformComposite {
		return h.compositeAvailableModels(ctx, &group.ID)
	}
	if group.Platform != service.PlatformOpenAI {
		return nil
	}
	if group.CodexModelsManifestConfig.Enabled && !h.gatewayService.ModelsRequireHealthCheck() && h.openAIGatewayService != nil {
		unfilteredGroup := *group
		unfilteredGroup.ModelAllowlist.Enabled = false
		response, _, err := h.openAIGatewayService.FetchPinnedOpenAIModelsList(ctx, &unfilteredGroup, h.maxAccountSwitches, "")
		if err != nil || response == nil {
			return nil
		}
		var catalog struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(response.Body, &catalog) != nil {
			return nil
		}
		models := make([]string, 0, len(catalog.Data))
		for _, item := range catalog.Data {
			models = append(models, item.ID)
		}
		return models
	}
	models := h.gatewayService.GetAvailableModels(ctx, &group.ID, service.PlatformOpenAI)
	if len(models) == 0 && !h.gatewayService.ModelsRequireHealthCheck() {
		return defaultModelIDsForPlatform(service.PlatformOpenAI)
	}
	return models
}

// PrepareAutoModelListing 保留客户端 ETag，供最终模型目录叠加 auto 后重新校验。
func (h *GatewayHandler) PrepareAutoModelListing(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformOpenAI {
		return
	}
	if model := c.Param("model"); model != "" && model != autoModelID {
		return
	}
	models := h.autoModelCatalog(c.Request.Context(), apiKey.Group)
	if !h.autoModelAvailable(c.Request.Context(), apiKey.Group, models) {
		return
	}
	c.Set(autoModelListingKey, true)
	c.Set(autoModelListingETagKey, c.GetHeader("If-None-Match"))
	c.Request.Header.Del("If-None-Match")
}

// AutoModelMiddleware 在合成路由解析前将虚拟模型解析为真实模型。
func (h *GatewayHandler) AutoModelMiddleware(resolver *service.CompositeRouteResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.Method != http.MethodPost || !autoModelRequestPath(c.FullPath()) {
			c.Next()
			return
		}
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.Group == nil ||
			(apiKey.Group.Platform != service.PlatformComposite && apiKey.Group.Platform != service.PlatformOpenAI) {
			c.Next()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to read request body"}})
			c.Abort()
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		if gjson.GetBytes(body, "model").String() != autoModelID {
			c.Next()
			return
		}
		modelNames := requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body)
		if len(modelNames) != 1 || modelNames[0] != autoModelID || !gjson.ValidBytes(body) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "model must have one unambiguous value"}})
			c.Abort()
			return
		}
		ctx, err := h.settingService.BindAutoModelRoutingPolicy(c.Request.Context())
		if err != nil {
			logger.FromContext(c.Request.Context()).Warn("gateway.auto_model_policy_unavailable", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model routing policy is unavailable"}})
			c.Abort()
			return
		}
		ctx = service.WithAutoModelRequestCapabilities(ctx, body)
		c.Request = c.Request.WithContext(ctx)
		models := h.autoModelCatalog(c.Request.Context(), apiKey.Group)
		if !h.autoModelAvailable(c.Request.Context(), apiKey.Group, models) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model routing is unavailable for this group"}})
			c.Abort()
			return
		}
		if resolver == nil {
			resolver = service.NewCompositeRouteResolver(nil)
		}
		routes, err := h.autoModelRoutableCandidates(
			ctx,
			apiKey.Group,
			resolver,
			c.FullPath(),
			body,
			h.autoModelEligibleCandidates(ctx, apiKey.Group, models),
		)
		if err != nil {
			logger.FromContext(c.Request.Context()).Warn("gateway.auto_model_candidates_unavailable", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model candidates are unavailable"}})
			c.Abort()
			return
		}
		if len(routes) == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model routing has no eligible model"}})
			c.Abort()
			return
		}
		candidates := make([]string, 0, len(routes))
		for _, route := range routes {
			candidates = append(candidates, route.model)
		}
		if h.billingCacheService != nil {
			subscription, _ := middleware2.GetSubscriptionFromContext(c)
			if billingErr := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); billingErr != nil {
				status, code, message, retryAfter := billingErrorDetails(billingErr)
				if retryAfter > 0 {
					c.Header("Retry-After", strconv.Itoa(retryAfter))
				}
				c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
				c.Abort()
				return
			}
		}
		model, err := h.chooseAutoModel(c, apiKey, body, candidates)
		if err != nil {
			if errors.Is(err, errAutoModelUsageRecord) {
				logger.FromContext(c.Request.Context()).Error("gateway.auto_model_usage_record_failed", zap.Error(err))
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "billing_error", "message": "Auto model decision usage unavailable"}})
			} else {
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "System One could not select an available model"}})
			}
			c.Abort()
			return
		}
		seedAutoModelFallback(c, model, routes)
		rewritten, err := sjson.SetBytes(body, "model", model)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "api_error", "message": "Failed to route auto model"}})
			c.Abort()
			return
		}
		c.Header("X-Sub2API-Selected-Model", model)
		requestmodel.ResetRequestBody(c.Request, rewritten)
		c.Next()
	}
}

func autoModelRequestPath(path string) bool {
	switch path {
	case "/v1/responses", "/responses", "/backend-api/codex/responses",
		"/v1/responses/*subpath", "/responses/*subpath", "/backend-api/codex/responses/*subpath",
		"/v1/chat/completions", "/chat/completions":
		return true
	default:
		return false
	}
}

func autoModelEndpoint(path string) string {
	if strings.HasSuffix(path, "/chat/completions") {
		return service.CompositeRouteEndpointChatCompletions
	}
	return service.CompositeRouteEndpointResponses
}

func (h *GatewayHandler) chooseAutoModel(c *gin.Context, apiKey *service.APIKey, body []byte, candidates []string) (string, error) {
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	ctx := c.Request.Context()
	criteria := make(map[string]string, len(candidates))
	for _, model := range candidates {
		platform, _ := service.DetectModelPlatform(model)
		criteria[model] = fmt.Sprintf("%s text model %s", platform, model)
	}
	request, err := json.Marshal(map[string]any{
		"model": jev_api.ModelID,
		"state": autoModelState(body),
		"questions": map[string]any{
			"model": map[string]any{
				"type":         "choice",
				"instructions": "Select the best available text model for this request. Consider task complexity, coding and tool needs, and efficiency. Choose exactly one candidate.",
				"criteria":     criteria,
			},
		},
	})
	if err != nil {
		return "", err
	}
	for _, target := range []struct{ model, platform string }{
		{jev_api.LayaModelID, service.PlatformLaya},
		{jev_api.ModelID, service.PlatformJev},
	} {
		selection, selectErr := h.selectSystemOneAccount(ctx, apiKey.GroupID, target.model, target.platform, apiKey.UserID)
		if selectErr != nil || selection == nil || selection.Account == nil {
			logger.FromContext(ctx).Debug("gateway.auto_model_decision_account_unavailable",
				zap.String("decision_model", target.model),
				zap.String("platform", target.platform),
				zap.Error(selectErr))
			continue
		}
		model, relayErr := relayAutoModelDecision(ctx, selection, request, target.model, criteria)
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		if relayErr == nil {
			usageRequest := request
			if target.model != jev_api.ModelID {
				usageRequest, err = jev_api.RewriteModel(request, target.model)
				if err != nil {
					return "", err
				}
			}
			if err := h.recordAutoModelDecision(c, apiKey, selection.Account, target.model, usageRequest); err != nil {
				return "", fmt.Errorf("%w: %v", errAutoModelUsageRecord, err)
			}
			return model, nil
		}
		logger.FromContext(ctx).Warn("gateway.auto_model_decision_failed",
			zap.String("decision_model", target.model),
			zap.String("platform", target.platform),
			zap.Error(relayErr))
		if errors.Is(relayErr, errAutoModelDecisionRejected) {
			return "", relayErr
		}
	}
	return "", fmt.Errorf("no valid System One model decision")
}

func (h *GatewayHandler) recordAutoModelDecision(c *gin.Context, apiKey *service.APIKey, account *service.Account, decisionModel string, request []byte) error {
	if h.apiKeyService == nil || h.gatewayService == nil {
		return nil
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	input := &service.RecordUsageInput{
		Result: &service.ForwardResult{
			RequestID: "systemone:auto:" + uuid.NewString(),
			Model:     decisionModel,
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
		QuotaPlatform:      account.Platform,
		RequestPayloadHash: service.HashUsageRequestPayload(request),
	}
	return h.gatewayService.RecordUsage(c.Request.Context(), input)
}

func relayAutoModelDecision(ctx context.Context, selection *service.AccountSelectionResult, request []byte, decisionModel string, candidates map[string]string) (string, error) {
	if !selection.Acquired && selection.ReleaseFunc == nil {
		return "", fmt.Errorf("System One account is busy")
	}
	account := selection.Account
	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = systemOneAccountBaseURL(account)
	}
	if baseURL == "" {
		return "", fmt.Errorf("System One account has no upstream address")
	}
	if decisionModel != jev_api.ModelID {
		var err error
		request, err = jev_api.RewriteModel(request, decisionModel)
		if err != nil {
			return "", err
		}
	}
	apiKeyValue, _ := account.Credentials["api_key"].(string)
	status, payload, err := jev_api.RelaySystemOne(ctx, baseURL, apiKeyValue, request, http.DefaultClient)
	if err != nil {
		return "", fmt.Errorf("System One decision request failed")
	}
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError &&
		status != http.StatusRequestTimeout && status != http.StatusTooManyRequests {
		return "", errAutoModelDecisionRejected
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", fmt.Errorf("System One decision request failed")
	}
	var response struct {
		Answers map[string]struct {
			Choice string `json:"choice"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", err
	}
	model := response.Answers["model"].Choice
	if _, ok := candidates[model]; !ok {
		return "", fmt.Errorf("System One selected an unknown model")
	}
	return model, nil
}

func autoModelState(body []byte) map[string]string {
	state := make(map[string]string)
	if instructions := autoModelText(gjson.GetBytes(body, "instructions")); instructions != "" {
		state["instructions"] = trimAutoModelText(instructions, autoModelStateByteLimit)
	}
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		input = gjson.GetBytes(body, "messages")
	}
	if prompt := autoModelText(input); prompt != "" {
		if len(prompt) > autoModelStateByteLimit {
			prompt = prompt[len(prompt)-autoModelStateByteLimit:]
		}
		state["request"] = prompt
	}
	tools := make([]string, 0)
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		name := tool.Get("name").String()
		if name == "" {
			name = tool.Get("function.name").String()
		}
		if name != "" {
			tools = append(tools, name)
		}
	}
	if len(tools) > 0 {
		state["tools"] = strings.Join(tools, ", ")
	}
	if effort := gjson.GetBytes(body, "reasoning.effort").String(); effort != "" {
		state["reasoning_effort"] = effort
	}
	return state
}

func trimAutoModelText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func autoModelText(value gjson.Result) string {
	switch value.Type {
	case gjson.String:
		return value.String()
	case gjson.JSON:
		if value.IsArray() {
			parts := make([]string, 0)
			for _, item := range value.Array() {
				if part := autoModelText(item); part != "" {
					parts = append(parts, part)
				}
			}
			return strings.Join(parts, "\n")
		}
		if text := value.Get("text"); text.Exists() {
			return autoModelText(text)
		}
		if content := value.Get("content"); content.Exists() {
			return autoModelText(content)
		}
	}
	return ""
}
