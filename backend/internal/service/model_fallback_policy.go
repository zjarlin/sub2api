package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/tidwall/gjson"
)

const SettingKeyModelFallbackPolicy = "model_fallback_policy"

const maxModelFallbackModels = 256

const AutoModelExcludedReason GatewayFailureReason = "auto_model_excluded"
const AutoModelCapabilityMismatchReason GatewayFailureReason = "auto_model_capability_mismatch"

const autoModelToolCapabilityBlockExtraKeyPrefix = "auto_tool_capability_block:"
const autoModelToolCapabilityBlockDuration = 30 * time.Minute

// 档位按数组顺序从高到低排列；模型 ID 精确匹配，不猜测未评级模型的能力。
type ModelCapabilityTier struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

type ModelFallbackPolicy struct {
	Enabled bool                  `json:"enabled"`
	Tiers   []ModelCapabilityTier `json:"tiers"`
}

type ModelFallbackCandidate struct {
	Model string
	Tier  string
}

type autoModelRoutingPolicyContextKey struct{}
type autoModelRequestCapabilitiesContextKey struct{}

type autoModelRequestCapabilities struct {
	ask            bool
	tools          bool
	images         bool
	visionFallback bool
}

type autoModelToolCapabilityBlock struct {
	Model        string    `json:"model"`
	ObservedAt   time.Time `json:"observed_at"`
	BlockedUntil time.Time `json:"blocked_until"`
}

type autoModelRoutingPolicy struct {
	highestExcluded map[string]struct{}
	excluded        map[string]struct{}
	ranks           map[string]int
	aliases         *ModelAliasPolicy
	policy          *AutoModelPolicy
}

// 采用公开榜单最高已测推理强度的粗粒度分段，依据和局限见 docs/model-fallback-tiers.md。
func DefaultModelFallbackPolicy() *ModelFallbackPolicy {
	return &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "AA 50+", Models: []string{"gpt-6-astra"}},
		{Name: "AA 40–49", Models: []string{"gpt-5.6-sol", "glm-5.3", "z-ai/glm-5.3", "kimi-k3", "moonshotai/kimi-k3", "gpt-5.6-terra", "glm-5.3-flash", "z-ai/glm-5.3-flash"}},
		{Name: "AA 30–39", Models: []string{"deepseek-v4.1-flash", "gpt-5.6-luna", "agnes-3.0-flash", "agnes-2.5-pro-beta", "qwen3.8-27b", "gpt-5.3-codex"}},
		{Name: "AA 20–29", Models: []string{"agnes-2.5-pro-alpha"}},
	}}
}

func (p *ModelFallbackPolicy) Validate() error {
	if p == nil || len(p.Tiers) > 12 || (p.Enabled && len(p.Tiers) == 0) {
		return fmt.Errorf("tiers must contain 1–12 tiers when enabled")
	}
	seen := make(map[string]bool)
	names := make(map[string]bool)
	for _, tier := range p.Tiers {
		if strings.TrimSpace(tier.Name) != tier.Name || tier.Name == "" || len(tier.Name) > 80 || names[tier.Name] || len(tier.Models) == 0 {
			return fmt.Errorf("each tier requires a unique name and at least one model")
		}
		names[tier.Name] = true
		for _, model := range tier.Models {
			if model == "" || len(model) > 200 || strings.ContainsAny(model, " \t\n\r*") || seen[model] {
				return fmt.Errorf("model IDs must be unique, nonempty and exact: %q", model)
			}
			seen[model] = true
		}
	}
	if len(seen) > maxModelFallbackModels {
		return fmt.Errorf("at most %d models can participate in fallback", maxModelFallbackModels)
	}
	return nil
}

// 先试同档和低档，耗尽后从最近的高档逐档向上补偿；候选固定且不循环。
func (p *ModelFallbackPolicy) Candidates(model string) []ModelFallbackCandidate {
	if p == nil || !p.Enabled {
		return nil
	}
	start := slices.IndexFunc(p.Tiers, func(t ModelCapabilityTier) bool { return slices.Contains(t.Models, model) })
	if start < 0 {
		return nil
	}
	var candidates []ModelFallbackCandidate
	tiers := slices.Clone(p.Tiers[start:])
	for i := start - 1; i >= 0; i-- {
		tiers = append(tiers, p.Tiers[i])
	}
	for _, tier := range tiers {
		for _, next := range tier.Models {
			if next != model {
				candidates = append(candidates, ModelFallbackCandidate{Model: next, Tier: tier.Name})
			}
		}
	}
	return candidates
}

func (s *SettingService) GetModelFallbackPolicy(ctx context.Context) (*ModelFallbackPolicy, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultModelFallbackPolicy(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyModelFallbackPolicy)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return DefaultModelFallbackPolicy(), nil
	}
	if err != nil {
		return nil, err
	}
	var policy ModelFallbackPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("decode model fallback policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *SettingService) SetModelFallbackPolicy(ctx context.Context, policy *ModelFallbackPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyModelFallbackPolicy, string(data))
}

// Auto 请求固定最高档、黑名单与别名快照；关闭降级开关不解除这些限制。
func (s *SettingService) BindAutoModelRoutingPolicy(ctx context.Context) (context.Context, error) {
	if _, bound := ctx.Value(autoModelRoutingPolicyContextKey{}).(*autoModelRoutingPolicy); bound {
		return ctx, nil
	}
	autoPolicy, err := s.GetAutoModelPolicy(ctx)
	if err != nil {
		return ctx, err
	}
	policy, err := s.GetModelFallbackPolicy(ctx)
	if err != nil {
		return ctx, err
	}
	if policy == nil || len(policy.Tiers) == 0 {
		return ctx, fmt.Errorf("auto model routing requires a highest capability tier")
	}
	aliases, err := s.GetModelAliasPolicy(ctx)
	if err != nil {
		return ctx, err
	}
	aliasSnapshot := &ModelAliasPolicy{Groups: make([]ModelAliasGroup, len(aliases.Groups))}
	for i, group := range aliases.Groups {
		aliasSnapshot.Groups[i] = ModelAliasGroup{Canonical: group.Canonical, Aliases: slices.Clone(group.Aliases)}
	}
	excluded := make(map[string]struct{}, len(policy.Tiers[0].Models))
	for _, model := range policy.Tiers[0].Models {
		excluded[aliasSnapshot.Canonicalize(model)] = struct{}{}
	}
	highestExcluded := make(map[string]struct{}, len(excluded))
	for model := range excluded {
		highestExcluded[model] = struct{}{}
	}
	for _, group := range aliasSnapshot.Groups {
		if autoPolicy.excludes(group.Canonical) || slices.ContainsFunc(group.Aliases, autoPolicy.excludes) {
			excluded[group.Canonical] = struct{}{}
		}
	}
	ranks := make(map[string]int)
	for rank, tier := range policy.Tiers {
		for index, model := range tier.Models {
			ranks[aliasSnapshot.Canonicalize(model)] = rank*1000 + index
		}
	}
	snapshot := &autoModelRoutingPolicy{highestExcluded: highestExcluded, excluded: excluded, aliases: aliasSnapshot, policy: autoPolicy, ranks: ranks}
	ctx = WithModelAliases(ctx, aliasSnapshot)
	return context.WithValue(ctx, autoModelRoutingPolicyContextKey{}, snapshot), nil
}

// IsAutoModelRouting 表示当前请求已绑定 Auto 路由策略。
func IsAutoModelRouting(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, bound := ctx.Value(autoModelRoutingPolicyContextKey{}).(*autoModelRoutingPolicy)
	return bound
}

// WithAutoModelRequestCapabilities 固定本次 Auto 请求依赖的能力，供候选预检和实际调度共用。
func WithAutoModelRequestCapabilities(ctx context.Context, body []byte) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	capabilities := autoModelRequestCapabilities{
		ask:    gjson.GetBytes(body, "model").String() == "ask",
		tools:  gjson.GetBytes(body, "tools.#").Int() > 0,
		images: autoModelInputHasImages(gjson.GetBytes(body, "input")) || autoModelInputHasImages(gjson.GetBytes(body, "messages")),
	}
	return context.WithValue(ctx, autoModelRequestCapabilitiesContextKey{}, capabilities)
}

// 文本适配器的提示词模拟工具不视为原生工具能力，只参与 Ask 调度。
func AutoModelPlatformAllowed(ctx context.Context, platform string) bool {
	if platform != PlatformDoubao && platform != PlatformDeepseekWeb && platform != PlatformCursor && platform != PlatformWindsurf {
		return true
	}
	if ctx == nil {
		return false
	}
	capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
	return capabilities.ask
}

func autoModelRequestNeedsTools(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
	return capabilities.tools
}

// 暴露已解析的工具调用能力，供虚拟模型路由复用。
func AutoModelRequestNeedsTools(ctx context.Context) bool {
	return autoModelRequestNeedsTools(ctx)
}

// 暴露已解析的图像输入能力，供虚拟模型路由复用。
func AutoModelRequestNeedsImages(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
	return capabilities.images
}

func autoModelToolCapabilityBlockKey(model string) string {
	model = normalizeUnsupportedModelKey(model)
	if model == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(model))
	return fmt.Sprintf("%s%x", autoModelToolCapabilityBlockExtraKeyPrefix, digest)
}

func newAutoModelToolCapabilityBlock(model string, now time.Time) (string, autoModelToolCapabilityBlock, bool) {
	model = normalizeUnsupportedModelKey(model)
	key := autoModelToolCapabilityBlockKey(model)
	if key == "" {
		return "", autoModelToolCapabilityBlock{}, false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return key, autoModelToolCapabilityBlock{
		Model:        model,
		ObservedAt:   now,
		BlockedUntil: now.Add(autoModelToolCapabilityBlockDuration),
	}, true
}

// AutoModelToolCapabilityBlocked 只约束带工具的 Auto 请求，不影响显式模型或纯文本请求。
func (a *Account) AutoModelToolCapabilityBlocked(requestedModel string, now time.Time) bool {
	if a == nil || a.Extra == nil {
		return false
	}
	canonicalModel := canonicalOpenAIAccountSchedulingModel(a, requestedModel)
	if a.IsOpenAIPassthroughEnabled() {
		canonicalModel = unsupportedModelKeyForAccount(a, requestedModel)
	}
	key := autoModelToolCapabilityBlockKey(canonicalModel)
	if key == "" {
		return false
	}
	raw, exists := a.Extra[key]
	if !exists || raw == nil {
		return false
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	var block autoModelToolCapabilityBlock
	if json.Unmarshal(body, &block) != nil || block.Model != normalizeUnsupportedModelKey(canonicalModel) || block.BlockedUntil.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	return now.Before(block.BlockedUntil)
}

// 仅约束已绑定的 Auto 请求，同时检查原始模型、规范模型与黑名单规则。
func AutoModelAllowed(ctx context.Context, models ...string) bool {
	if ctx == nil {
		return true
	}
	snapshot, _ := ctx.Value(autoModelRoutingPolicyContextKey{}).(*autoModelRoutingPolicy)
	if snapshot == nil {
		return true
	}
	for _, model := range models {
		canonical := snapshot.aliases.Canonicalize(model)
		capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
		if capabilities.ask {
			if _, excluded := snapshot.highestExcluded[canonical]; excluded {
				return false
			}
			continue
		}
		if _, excluded := snapshot.excluded[canonical]; excluded || snapshot.policy.excludes(model) || snapshot.policy.excludes(canonical) {
			return false
		}
	}
	return true
}

func (e *UpstreamFailoverError) IsAutoModelExcluded() bool {
	return e != nil && e.Reason == AutoModelExcludedReason &&
		e.Scope == GatewayFailureScopeRequest && e.SkipAccountScheduleFailure
}

// 最终转发仍需校验；本地成本限制允许外层换号，但不计为账号故障。
func checkAutoModelUpstream(ctx context.Context, models ...string) error {
	if AutoModelAllowed(ctx, models...) {
		return nil
	}
	return &UpstreamFailoverError{
		StatusCode:                 http.StatusServiceUnavailable,
		Stage:                      GatewayFailureStageInference,
		Scope:                      GatewayFailureScopeRequest,
		Reason:                     AutoModelExcludedReason,
		ClientStatusCode:           http.StatusServiceUnavailable,
		ClientMessage:              "Auto model routing excludes the selected upstream model",
		SkipAccountScheduleFailure: true,
	}
}

// 只在同名账号耗尽后读取一次设置及分组目录，成功请求不增加数据库查询。
func (s *OpenAIGatewayService) ModelFallbackCandidates(ctx context.Context, groupID *int64, model string, body []byte) ([]ModelFallbackCandidate, error) {
	policy, err := s.settingService.GetModelFallbackPolicy(ctx)
	if err != nil {
		return nil, err
	}
	aliases, err := s.settingService.GetModelAliasPolicy(ctx)
	if err != nil {
		return nil, err
	}
	canonical := aliases.Canonicalize(model)
	policy = aliases.NormalizeFallback(policy)
	ctx = WithModelAliases(ctx, aliases)
	candidates := policy.Candidates(canonical)
	if len(candidates) == 0 {
		return nil, nil
	}
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	useCompactModelMapping := false
	if forwardModel, ok := openAIForwardModelFromContext(ctx); ok {
		useCompactModelMapping = forwardModel.useCompactModelMapping
	}
	return slices.DeleteFunc(candidates, func(candidate ModelFallbackCandidate) bool {
		mapping, restricted := s.ResolveChannelMappingAndRestrict(ctx, groupID, candidate.Model)
		if restricted {
			return true
		}
		forward := candidate.Model
		if mapping.Mapped {
			forward = mapping.MappedModel
		}
		if !AutoModelAllowed(ctx, candidate.Model, forward) {
			return true
		}
		return !slices.ContainsFunc(accounts, func(account Account) bool {
			upstream := ResolveOpenAIAccountUpstreamModelForRequest(&account, forward, useCompactModelMapping)
			return AutoModelAllowed(ctx, upstream) && account.IsModelSupported(forward) && ModelFallbackAccountCompatible(&account, forward, body)
		})
	}), nil
}

// AutoModelAccountCompatible 要求候选至少存在一个可调度且满足本次请求能力的账号。
func (s *GatewayService) AutoModelAccountCompatible(ctx context.Context, groupID *int64, platform, model string, body []byte) (bool, error) {
	if s == nil || s.accountRepo == nil || strings.TrimSpace(model) == "" {
		return false, nil
	}
	snapshot, bound := ctx.Value(autoModelAccountsKey{}).(*autoModelInventory)
	var accounts []Account
	var err error
	if bound && groupID != nil && snapshot.groupID == *groupID {
		accounts = snapshot.accounts
	} else {
		if groupID != nil {
			accounts, err = s.accountRepo.ListSchedulableByGroupID(ctx, *groupID)
		} else {
			accounts, err = s.accountRepo.ListSchedulable(ctx)
		}
		accounts = accountsWithModelAliases(ctx, accounts)
	}
	if err != nil {
		return false, err
	}
	platform = NormalizeOpenAICompatiblePlatform(platform)
	mapping, restricted := s.ResolveChannelMappingAndRestrict(ctx, groupID, model)
	if restricted {
		return false, nil
	}
	forwardModel := model
	if mapping.Mapped {
		forwardModel = mapping.MappedModel
	}
	if !AutoModelAllowed(ctx, model, forwardModel) {
		return false, nil
	}
	requiresTools := gjson.GetBytes(body, "tools.#").Int() > 0
	now := time.Now()
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() || !openAIAccountMatchesPlatform(account, platform) || !account.IsModelSupported(forwardModel) {
			continue
		}
		if requiresTools && account.AutoModelToolCapabilityBlocked(forwardModel, now) {
			continue
		}
		upstreamModel := ResolveOpenAIAccountUpstreamModelForRequest(account, forwardModel, false)
		if AutoModelAllowed(ctx, upstreamModel) && AutoModelRequestAccountCompatible(ctx, account, forwardModel, body) {
			return true, nil
		}
	}
	return false, nil
}

// 故障转移同时要求历史可跨模型重放，不能把原生续轮能力等同于可移植性。
func ModelFallbackAccountCompatible(account *Account, model string, body []byte) bool {
	return ModelFallbackRequestPortable(body) && ModelAccountCompatible(account, model, body)
}

// 候选预检与实际选号共用账号能力检查；已知上下文上限采用字节数保守估算，不裁剪历史。
func ModelAccountCompatible(account *Account, model string, body []byte) bool {
	return modelAccountCompatible(account, model, body, false)
}

func modelAccountCompatible(account *Account, model string, body []byte, assistedVision bool) bool {
	if account == nil || !modelAccountPreservesSearchTools(account, model, body) {
		return false
	}
	if account.IsCursor() || account.IsWindsurf() {
		if gjson.GetBytes(body, "tools.#").Int() > 0 {
			return false
		}
		for _, field := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens", "temperature", "top_p"} {
			value := gjson.GetBytes(body, field)
			if value.Exists() && value.Type != gjson.Null {
				return false
			}
		}
	}
	upstream := account.GetMappedModel(model)
	metadata, known := account.GetUpstreamModelMetadata(upstream)
	limit := metadata.MaxContextWindow
	if limit <= 0 {
		limit = metadata.ContextWindow
	}
	output := slices.Max([]int64{0, gjson.GetBytes(body, "max_output_tokens").Int(), gjson.GetBytes(body, "max_completion_tokens").Int(), gjson.GetBytes(body, "max_tokens").Int()})
	// Base64 图片不是文本 token；这里只估算文本上下文，图像 token 由实际视觉上游校验。
	textBytes := int64(len(body)) - modelInputImageDataBytes(gjson.GetBytes(body, "input")) - modelInputImageDataBytes(gjson.GetBytes(body, "messages"))
	if (limit > 0 && textBytes+output > limit) || (metadata.MaxOutputTokens > 0 && output > metadata.MaxOutputTokens) {
		return false
	}
	effort := gjson.GetBytes(body, "reasoning.effort").String()
	if effort == "" {
		effort = gjson.GetBytes(body, "reasoning_effort").String()
	}
	if len(metadata.SupportedReasoningLevels) > 0 && effort != "" && !slices.Contains(metadata.SupportedReasoningLevels, effort) {
		return false
	}
	if known && string(metadata.CodexToolCapabilities["supports_function_calling"]) == "false" && gjson.GetBytes(body, "tools.#").Int() > 0 {
		return false
	}
	return modelInputCompatible(gjson.GetBytes(body, "input"), account, model, assistedVision) && modelInputCompatible(gjson.GetBytes(body, "messages"), account, model, assistedVision)
}

// 请求级先排除不可移植输入；图片能力仍在每个候选账号上检查。
func ModelFallbackRequestPortable(body []byte) bool {
	return modelInputCompatible(gjson.GetBytes(body, "input"), nil, "", false) &&
		modelInputCompatible(gjson.GetBytes(body, "messages"), nil, "", false)
}

func modelInputImageDataBytes(value gjson.Result) int64 {
	if !value.IsObject() && !value.IsArray() {
		return 0
	}
	switch value.Get("type").String() {
	case "input_image", "image_url":
		imageURL := value.Get("image_url")
		if imageURL.IsObject() {
			imageURL = imageURL.Get("url")
		}
		if strings.HasPrefix(imageURL.String(), "data:image/") {
			return int64(len(imageURL.Raw))
		}
	case "image":
		if value.Get("source.type").String() == "base64" {
			return int64(len(value.Get("source.data").Raw))
		}
	}
	var size int64
	value.ForEach(func(_, child gjson.Result) bool {
		size += modelInputImageDataBytes(child)
		return true
	})
	return size
}

func modelInputCompatible(value gjson.Result, account *Account, model string, assistedVision bool) bool {
	if !value.IsObject() && !value.IsArray() {
		return true
	}
	if value.Get("role").String() == "assistant" && value.Get("audio.id").String() != "" {
		return false
	}
	switch value.Get("type").String() {
	case "input_image", "image_url", "image":
		if value.Get("file_id").Exists() {
			return false
		}
		if account != nil && !accountHasNativeVision(account, model) {
			if !assistedVision || value.Get("type").String() == "image" {
				return false
			}
		}
	case "input_file", "file", "input_audio", "audio", "video", "video_url", "input_video", "item_reference":
		return false
	case "redacted_thinking":
		return false
	case "thinking":
		if value.Get("signature").String() != "" {
			return false
		}
	case "reasoning":
		// 原生 Responses 可以接收完整推理项；已确认走 Responses→Chat 桥接的
		// OpenAI 账号会在转换时回注缓存/占位 reasoning_content，因此同样可用。
		// 未知协议与其它平台仍保持严格限制；通用跨模型 fallback 另有
		// ModelFallbackRequestPortable 作为可移植性门槛。
		if value.Get("encrypted_content").String() != "" &&
			(account == nil || !account.IsOpenAI() ||
				(!account.UsesOpenAICodexProtocol() && openai_compat.ResolveResponsesSupport(account.Extra) == openai_compat.ResponsesSupportUnknown)) {
			return false
		}
	}
	compatible := true
	value.ForEach(func(_, child gjson.Result) bool {
		compatible = modelInputCompatible(child, account, model, assistedVision)
		return compatible
	})
	return compatible
}
