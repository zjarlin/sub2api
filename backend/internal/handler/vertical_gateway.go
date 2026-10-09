package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// 垂直能力在 Auto 选模前运行，显式模型 ID 也使用同一条能力路由。
func (h *GatewayHandler) VerticalIntentMiddleware(resolver *service.CompositeRouteResolver, images, videos gin.HandlerFunc) gin.HandlerFunc {
	if resolver == nil {
		resolver = service.NewCompositeRouteResolver(nil)
	}
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.Method != http.MethodPost || !verticalRequestPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		key, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || key == nil || key.Group == nil || h.settingService == nil {
			c.Next()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			c.Next()
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		text := verticalUserText(body)
		modelField := gjson.GetBytes(body, "model")
		model := modelField.String()
		explicitKind := verticalExplicitKind(text)
		names := requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body)
		if text == "" || len(text) > 16*1024 || model == "" || modelField.Type != gjson.String ||
			len(names) != 1 || names[0] != model || explicitKind == "" {
			c.Next()
			return
		}
		policy, err := h.settingService.GetAutoModelPolicy(c.Request.Context())
		if err != nil || !policy.VerticalPolicy().Enabled {
			c.Next()
			return
		}
		vertical := policy.VerticalPolicy()
		subject, ok := middleware2.GetAuthSubjectFromContext(c)
		if !ok {
			c.Next()
			return
		}
		protocol := service.ContentModerationProtocolOpenAIResponses
		if strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
			protocol = service.ContentModerationProtocolOpenAIChat
		}
		setOpsRequestContext(c, model, gjson.GetBytes(body, "stream").Bool())
		if decision := h.checkSecurityAudit(c, logger.FromContext(c.Request.Context()), key, subject, protocol, model, body); decision != nil && !decision.AllowNextStage {
			h.openAISecurityAuditError(c, decision)
			c.Abort()
			return
		}
		kind := explicitKind
		if kind != "translation" {
			payload, err := verticalDecisionRequest(text)
			if err != nil {
				c.Next()
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(vertical.TimeoutMS)*time.Millisecond)
			status, decision := h.turnActionDecision(c, ctx, payload)
			cancel()
			kind = ""
			if status >= 200 && status < 300 {
				kind = verticalDecisionKind(decision, vertical.MinConfidence)
			} else {
				logger.FromContext(c.Request.Context()).Warn("gateway.vertical_classifier_unavailable", zap.Int("status", status))
			}
		}
		if kind == "" || kind != explicitKind {
			c.Next()
			return
		}
		if kind == "translation" {
			request := verticalTranslationRequest(text)
			if request == nil {
				c.Next()
				return
			}
			h.executeVerticalTranslation(c, key, model, body, request)
			c.Abort()
			return
		}
		selected, platform, err := h.verticalMediaModel(c.Request.Context(), key.Group, resolver, kind, vertical)
		if err != nil || selected == "" || (kind == "image_generation" && images == nil) || (kind == "video_generation" && videos == nil) {
			c.Next()
			return
		}
		h.executeVerticalMedia(c, key, model, body, text, kind, selected, platform, resolver, images, videos)
		c.Abort()
	}
}

func verticalRequestPath(path string) bool {
	switch path {
	case "/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/chat/completions", "/chat/completions":
		return true
	default:
		return false
	}
}

func (h *GatewayHandler) verticalObservation(c *gin.Context, key *service.APIKey, requested, selected string, operation *service.VerticalOperation) (*service.AutoModelRouteObservation, func()) {
	c.Set("virtual_model_id", requested)
	c.Set(verticalOperationKey, operation)
	c.Header("X-Sub2API-Selected-Model", selected)
	c.Header("X-Sub2API-Capability", operation.Kind)
	finish := h.observeAutoModelRoute(c, key, selected)
	if value, ok := c.Get(autoRouteObservationKey); ok {
		return value.(*service.AutoModelRouteObservation), finish
	}
	return &service.AutoModelRouteObservation{SelectedModel: selected, Operation: operation, State: "selected"}, finish
}

func (h *GatewayHandler) executeVerticalTranslation(c *gin.Context, key *service.APIKey, model string, body []byte, request *translate.TranslateRequest) {
	operation := &service.VerticalOperation{Kind: "translation", SourceLanguage: request.SourceLang, TargetLanguage: request.TargetLang}
	route, finish := h.verticalObservation(c, key, model, "translation", operation)
	defer finish()
	if h.billingCacheService == nil {
		verticalFailure(c, route, http.StatusServiceUnavailable, "Translation billing eligibility unavailable")
		return
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.QuotaPlatform(c.Request.Context(), key)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", fmt.Sprint(retryAfter))
		}
		route.State = "failed"
		c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
		return
	}
	if h.concurrencyHelper != nil {
		subject, _ := middleware2.GetAuthSubjectFromContext(c)
		started := false
		release, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, false, &started)
		if err != nil {
			route.State = "failed"
			h.handleConcurrencyError(c, err, "user", false)
			return
		}
		if release != nil {
			defer release()
		}
	}
	aggregator, err := h.settingService.GetTranslationAggregator(c.Request.Context())
	if err != nil {
		verticalFailure(c, route, http.StatusServiceUnavailable, "Translation configuration unavailable")
		return
	}
	route.State = "responding"
	persistVerticalState(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	result, err := aggregator.Translate(ctx, request)
	if err != nil || result == nil || len(result.Translations) != len(request.Text) {
		verticalFailure(c, route, http.StatusBadGateway, "Translation providers could not complete this request")
		return
	}
	texts := make([]string, 0, len(result.Translations))
	for _, translation := range result.Translations {
		if translation.Text == "" {
			verticalFailure(c, route, http.StatusBadGateway, "Translation provider returned empty text")
			return
		}
		texts = append(texts, translation.Text)
		if operation.SourceLanguage == "" {
			operation.SourceLanguage = translation.DetectedLanguage
		}
	}
	operation.Provider = result.Provider
	route.ResolvedModel = result.Provider
	route.State = "completed"
	writeVerticalReply(c, body, model, strings.Join(texts, "\n\n"), nil)
}

func (h *GatewayHandler) verticalMediaModel(ctx context.Context, group *service.Group, resolver *service.CompositeRouteResolver, kind string, policy service.VerticalRoutingPolicy) (string, string, error) {
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, group.ID)
	if err != nil {
		return "", "", err
	}
	configured := policy.ImageModel
	endpoint := service.CompositeRouteEndpointImages
	if kind == "video_generation" {
		configured = policy.VideoModel
		endpoint = service.CompositeRouteEndpointAny
	}
	aliases := service.ModelAliasesFromContext(ctx)
	models = aliases.CanonicalIDs(models)
	configured = aliases.Canonicalize(configured)
	slices.Sort(models)
	if configured != "" {
		if !slices.Contains(models, configured) {
			return "", "", nil
		}
		models = []string{configured}
	}
	for _, model := range models {
		name := strings.ToLower(model)
		if configured == "" && ((kind == "image_generation" && !strings.Contains(name, "image") && !strings.Contains(name, "dall-e")) ||
			(kind == "video_generation" && !strings.Contains(name, "video"))) {
			continue
		}
		if strings.ContainsAny(name, "*") || (group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(model)) {
			continue
		}
		decision, err := resolver.Resolve(ctx, group.ID, model, endpoint)
		if err != nil {
			return "", "", err
		}
		platform := group.Platform
		if decision.Matched {
			platform = decision.TargetPlatform
		}
		if platform == service.PlatformComposite {
			for _, source := range service.AutoModelInventoryPlatforms(ctx, model) {
				if source == service.PlatformOpenAI || source == service.PlatformGrok {
					platform = source
					break
				}
			}
		}
		mediaModel := model
		if decision.Matched && decision.UpstreamModel != "" {
			mediaModel = decision.UpstreamModel
		}
		// 目录中的 image 名称不保证支持当前生成端点，复用现有处理器的模型识别。
		dashScopeImage := kind == "image_generation" && service.IsDashScopeChatImageModel(mediaModel)
		if kind == "image_generation" && platform == service.PlatformOpenAI && !dashScopeImage &&
			!service.IsExplicitImageGenerationIntent("/v1/responses", mediaModel, nil) {
			continue
		}
		if (kind == "image_generation" && (platform == service.PlatformOpenAI || platform == service.PlatformGrok)) ||
			(kind == "video_generation" && platform == service.PlatformGrok) {
			return model, platform, nil
		}
	}
	return "", "", nil
}

func verticalFailure(c *gin.Context, route *service.AutoModelRouteObservation, status int, message string) {
	route.State = "failed"
	c.JSON(status, gin.H{"error": gin.H{"type": "vertical_service_error", "message": message}})
}

// 避免将提示词、原文、凭据或媒体二进制写入路由观测记录。
func verticalMediaRequest(model, prompt, kind string) []byte {
	payload := map[string]any{"model": model, "prompt": prompt}
	if kind == "image_generation" {
		payload["n"] = 1
		payload["response_format"] = "url"
	}
	body, _ := json.Marshal(payload)
	return body
}

// 百炼/Wan 图片模型要求 content 为列表，返回 output.choices[].message.content[].image。
func verticalDashScopeImageRequest(model, prompt string) []byte {
	payload := map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": prompt}}}},
	}
	body, _ := json.Marshal(payload)
	return body
}
