package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const (
	autoModelID               = "auto"
	askModelID                = "ask"
	autoModelListingKey       = "auto_model_listing"
	autoModelListingModelsKey = "auto_model_listing_models"
	autoModelListingETagKey   = "auto_model_listing_etag"
)

type autoModelRouteCandidate struct {
	model          string
	targetPlatform string
	upstreamModel  string
}

func prependAutoModel(models []string) []string {
	return prependVirtualModels(models, autoModelID, askModelID)
}

func prependVirtualModels(models []string, virtualModels ...string) []string {
	result := make([]string, 0, len(models)+len(virtualModels))
	result = append(result, virtualModels...)
	for _, model := range models {
		if !isVirtualModelID(model) {
			result = append(result, model)
		}
	}
	return result
}

func (h *GatewayHandler) availableVirtualModelIDs(ctx context.Context, group *service.Group, models []string) []string {
	virtualModels := make([]string, 0, 2)
	if h.autoModelAvailable(ctx, group, models) {
		virtualModels = append(virtualModels, autoModelID)
	}
	if h.askModelAvailable(ctx, group, models) {
		virtualModels = append(virtualModels, askModelID)
	}
	return virtualModels
}

func (h *GatewayHandler) prependAvailableVirtualModels(ctx context.Context, group *service.Group, models []string) []string {
	virtualModels := h.availableVirtualModelIDs(ctx, group, models)
	if len(virtualModels) == 0 {
		return models
	}
	return prependVirtualModels(models, virtualModels...)
}

func isVirtualModelID(model string) bool {
	return model == autoModelID || model == askModelID
}

// 模型名称不决定协议平台；这里只排除决策模型和专用模型。
func autoModelCandidates(models []string) []string {
	candidates := make([]string, 0, len(models))
	for _, model := range service.FilterCodexModelIDsForGroup(models, nil) {
		if isVirtualModelID(model) || !autoModelTextCandidate(model) {
			continue
		}
		candidates = append(candidates, model)
	}
	return candidates
}

func autoModelCandidatesForGroup(_ *service.Group, models []string) []string {
	return autoModelCandidates(models)
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
	platform, _ := service.DetectModelPlatform(model)
	if isVirtualModelID(model) || strings.Contains(model, "*") || service.IsSystemOneDecisionPlatform(platform) {
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

func autoModelTextPlatform(platform string) bool {
	switch platform {
	case service.PlatformOpenAI, service.PlatformGrok, service.PlatformKimi,
		service.PlatformZhipu, service.PlatformDeepseek, service.PlatformMiniMax,
		service.PlatformOpenCodeGo, service.PlatformDoubao, service.PlatformDeepseekWeb, service.PlatformCursor, service.PlatformTraework,
		service.PlatformWorkbuddy, service.PlatformVibex, service.PlatformZcode,
		service.PlatformQoder:
		return true
	default:
		return false
	}
}

func (h *GatewayHandler) autoModelAvailable(ctx context.Context, group *service.Group, models []string) bool {
	return h.virtualModelAvailable(ctx, group, models, autoModelID)
}

func (h *GatewayHandler) askModelAvailable(ctx context.Context, group *service.Group, models []string) bool {
	return h.virtualModelAvailable(ctx, group, models, askModelID)
}

func (h *GatewayHandler) virtualModelAvailable(ctx context.Context, group *service.Group, models []string, virtualModel string) bool {
	ctx = service.WithAutoModelRequestCapabilities(ctx, []byte(`{"model":"`+virtualModel+`"}`))
	if h == nil || h.gatewayService == nil || group == nil ||
		(group.Platform != service.PlatformComposite && group.Platform != service.PlatformOpenAI) ||
		(group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(virtualModel)) {
		return false
	}
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(ctx)
	if err != nil {
		return false
	}
	ctx, models, err = h.gatewayService.BindAutoModelInventory(ctx, group.ID)
	if err != nil || len(h.autoModelEligibleCandidates(ctx, group, models)) == 0 {
		return false
	}
	platforms := h.gatewayService.GetSchedulablePlatforms(ctx, &group.ID)
	for platform := range platforms {
		if autoModelTextPlatform(platform) && service.AutoModelPlatformAllowed(ctx, platform) {
			return true
		}
	}
	return false
}

func (h *GatewayHandler) autoModelCatalog(ctx context.Context, group *service.Group) []string {
	if h == nil || h.gatewayService == nil || group == nil {
		return nil
	}
	_, models, err := h.gatewayService.BindAutoModelInventory(ctx, group.ID)
	if err != nil {
		logger.FromContext(ctx).Warn("gateway.auto_model_inventory_unavailable", zap.Error(err))
		return nil
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
	model := c.Param("model")
	if model != "" && !isVirtualModelID(model) {
		return
	}
	models := h.autoModelCatalog(c.Request.Context(), apiKey.Group)
	listed := h.availableVirtualModelIDs(c.Request.Context(), apiKey.Group, models)
	if model != "" {
		listed = nil
		if h.virtualModelAvailable(c.Request.Context(), apiKey.Group, models, model) {
			listed = []string{model}
		}
	}
	if len(listed) == 0 {
		return
	}
	c.Set(autoModelListingKey, true)
	c.Set(autoModelListingModelsKey, listed)
	c.Set(autoModelListingETagKey, c.GetHeader("If-None-Match"))
	c.Request.Header.Del("If-None-Match")
}

// AutoModelMiddleware 在合成路由解析前将虚拟模型解析为真实模型。
func (h *GatewayHandler) AutoModelMiddleware(resolver *service.CompositeRouteResolver) gin.HandlerFunc {
	if resolver == nil {
		resolver = service.NewCompositeRouteResolver(nil)
	}
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
		virtualModel := gjson.GetBytes(body, "model").String()
		if !isVirtualModelID(virtualModel) {
			c.Next()
			return
		}
		c.Set("virtual_model_id", virtualModel)
		modelNames := requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body)
		if len(modelNames) != 1 || modelNames[0] != virtualModel || !gjson.ValidBytes(body) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "model must have one unambiguous value"}})
			c.Abort()
			return
		}
		ctx, err := h.settingService.BindAutoModelRoutingPolicy(c.Request.Context())
		if err != nil {
			finishObservation := h.observeAutoModelRoute(c, apiKey, "")
			defer finishObservation()
			logger.FromContext(c.Request.Context()).Warn("gateway.auto_model_policy_unavailable", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model routing policy is unavailable"}})
			c.Abort()
			return
		}
		ctx = service.WithAutoModelRequestCapabilities(ctx, body)
		c.Request = c.Request.WithContext(ctx)
		if virtualModel == askModelID && service.AutoModelRequestNeedsTools(ctx) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "unsupported_request", "message": "ask supports conversation requests without tools"}})
			c.Abort()
			return
		}
		ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, apiKey.Group.ID)
		if err != nil {
			finishObservation := h.observeAutoModelRoute(c, apiKey, "")
			defer finishObservation()
			logger.FromContext(ctx).Warn("gateway.auto_model_inventory_unavailable", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model inventory is unavailable"}})
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		ctx, err = h.gatewayService.BindAutoModelVisionCapabilities(ctx, apiKey.Group)
		if err != nil {
			finishObservation := h.observeAutoModelRoute(c, apiKey, "")
			defer finishObservation()
			logger.FromContext(ctx).Warn("gateway.auto_model_vision_policy_unavailable", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model image assistance policy is unavailable"}})
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		if !h.virtualModelAvailable(ctx, apiKey.Group, models, virtualModel) {
			finishObservation := h.observeAutoModelRoute(c, apiKey, "")
			defer finishObservation()
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": virtualModel + " model routing is unavailable for this group"}})
			c.Abort()
			return
		}
		routes, plan, err := h.autoModelPlan(
			ctx,
			apiKey.Group,
			resolver,
			c.FullPath(),
			body,
			models,
		)
		if err != nil {
			finishObservation := h.observeAutoModelRoute(c, apiKey, "")
			defer finishObservation()
			logger.FromContext(c.Request.Context()).Warn("gateway.auto_model_candidates_unavailable", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model candidates are unavailable"}})
			c.Abort()
			return
		}
		if len(routes) == 0 {
			// Keep the rejected plan observable too: no model is selected yet, but
			// the per-candidate reasons are needed to diagnose request capabilities.
			c.Set(autoModelPlanKey, plan)
			finishObservation := h.observeAutoModelRoute(c, apiKey, "")
			defer finishObservation()
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "Auto model routing has no eligible model"}})
			c.Abort()
			return
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
		selected := routes[0]
		model := selected.model
		c.Set(autoModelPlanKey, plan)
		c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(ctx, service.CompositeRouteDecision{
			Matched: true, GroupID: apiKey.Group.ID, PublicModel: model,
			TargetPlatform: selected.targetPlatform, UpstreamModel: selected.upstreamModel,
			Endpoint: autoModelEndpoint(c.FullPath()), Source: service.CompositeRouteSourceAccount,
		}))
		seedAutoModelFallback(c, model, routes)
		rewritten, err := sjson.SetBytes(body, "model", selected.upstreamModel)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "api_error", "message": "Failed to route auto model"}})
			c.Abort()
			return
		}
		c.Header("X-Sub2API-Selected-Model", model)
		finishObservation := h.observeAutoModelRoute(c, apiKey, model)
		defer finishObservation()
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
