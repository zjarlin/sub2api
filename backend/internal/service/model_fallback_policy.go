package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

const SettingKeyModelFallbackPolicy = "model_fallback_policy"

const maxModelFallbackModels = 256

const AutoModelExcludedReason GatewayFailureReason = "auto_model_excluded"

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

type autoModelRoutingPolicy struct {
	excluded map[string]struct{}
	aliases  *ModelAliasPolicy
	policy   *AutoModelPolicy
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

// 从原请求所在档位开始；同档其他模型先于低档模型，不回升、不循环。
func (p *ModelFallbackPolicy) Candidates(model string) []ModelFallbackCandidate {
	if p == nil || !p.Enabled {
		return nil
	}
	start := slices.IndexFunc(p.Tiers, func(t ModelCapabilityTier) bool { return slices.Contains(t.Models, model) })
	if start < 0 {
		return nil
	}
	var candidates []ModelFallbackCandidate
	for _, tier := range p.Tiers[start:] {
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
	for _, group := range aliasSnapshot.Groups {
		if autoPolicy.excludes(group.Canonical) || slices.ContainsFunc(group.Aliases, autoPolicy.excludes) {
			excluded[group.Canonical] = struct{}{}
		}
	}
	snapshot := &autoModelRoutingPolicy{excluded: excluded, aliases: aliasSnapshot, policy: autoPolicy}
	ctx = WithModelAliases(ctx, aliasSnapshot)
	return context.WithValue(ctx, autoModelRoutingPolicyContextKey{}, snapshot), nil
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

// 换模型前及实际选号后均检查能力；已知上下文上限采用字节数保守估算，不裁剪历史。
func ModelFallbackAccountCompatible(account *Account, model string, body []byte) bool {
	if account == nil {
		return false
	}
	upstream := account.GetMappedModel(model)
	metadata, known := account.GetUpstreamModelMetadata(upstream)
	limit := metadata.MaxContextWindow
	if limit <= 0 {
		limit = metadata.ContextWindow
	}
	output := slices.Max([]int64{0, gjson.GetBytes(body, "max_output_tokens").Int(), gjson.GetBytes(body, "max_completion_tokens").Int(), gjson.GetBytes(body, "max_tokens").Int()})
	if (limit > 0 && int64(len(body))+output > limit) || (metadata.MaxOutputTokens > 0 && output > metadata.MaxOutputTokens) {
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
	return fallbackInputCompatible(gjson.GetBytes(body, "input"), account, model) && fallbackInputCompatible(gjson.GetBytes(body, "messages"), account, model)
}

// 请求级先排除不可移植输入；图片能力仍在每个候选账号上检查。
func ModelFallbackRequestPortable(body []byte) bool {
	return fallbackInputCompatible(gjson.GetBytes(body, "input"), nil, "") &&
		fallbackInputCompatible(gjson.GetBytes(body, "messages"), nil, "")
}

func fallbackInputCompatible(value gjson.Result, account *Account, model string) bool {
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
			return false
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
		if value.Get("encrypted_content").Exists() {
			return false
		}
	}
	compatible := true
	value.ForEach(func(_, child gjson.Result) bool {
		compatible = fallbackInputCompatible(child, account, model)
		return compatible
	})
	return compatible
}
