package service

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type visionFallbackCandidate struct {
	account *Account
	model   string
}

func visionFallbackEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Gateway.VisionFallback.Enabled
}

// 仅由已有能力证据认定原生视觉，辅助解析后的目录声明不能反过来成为证据。
func accountHasNativeVision(account *Account, model string) bool {
	if account == nil {
		return false
	}
	_, upstream := resolveOpenAIForwardMappedModels(account, model, false)
	if metadata, ok := account.GetUpstreamModelMetadata(upstream); ok && len(metadata.InputModalities) > 0 {
		return stringSliceContains(normalizeCodexInputModalities(metadata.InputModalities), "image")
	}
	// 第三方兼容端点不能只凭 GPT 型号名继承原生视觉能力，必须提供显式能力快照。
	if account.IsOpenAIApiKey() && !isOfficialOpenAIModelsBaseURL(account.GetOpenAIBaseURL()) {
		return false
	}
	return accountCodexModelSupportsImageInput(account, upstream)
}

// 只有在拿到显式的文本-only 能力声明（目录快照或真实读图探测失败）时才断言
// “纯文本”。模型名不是能力证据：deepseek-v4.1-flash 这类型号在多家上游都能直接
// 读图，按前缀判定会把图片从本可原生处理的模型上抽走，改走昂贵且易失败的辅助链路。
func accountHasKnownTextOnlyInput(account *Account, model string) bool {
	if account == nil {
		return false
	}
	_, upstream := resolveOpenAIForwardMappedModels(account, model, false)
	if metadata, ok := account.GetUpstreamModelMetadata(upstream); ok && len(metadata.InputModalities) > 0 {
		return !stringSliceContains(normalizeCodexInputModalities(metadata.InputModalities), "image")
	}
	// 没有任何证据时保持 unknown，不回退到模型家族/前缀假设。
	return false
}

// 所有缺少原生视觉证据的对话模型统一使用辅助，不按模型家族豁免。
func accountNeedsVisionFallback(account *Account, model string) bool {
	if account == nil || !visionFallbackPlatform(account.Platform) || accountHasNativeVision(account, model) {
		return false
	}
	_, upstream := resolveOpenAIForwardMappedModels(account, model, false)
	if strings.TrimSpace(upstream) == "" || isCodexDedicatedMediaModel(upstream) || isCodexDedicatedMediaModel(model) {
		return false
	}
	return true
}

func visionFallbackPlatform(platform string) bool {
	// 与 OpenAI 网关共用平台分类，内置适配器也必须先完成图片转描述。
	return (&Account{Platform: platform}).IsOpenAICompatible()
}

// 候选仅来自调用方分组的可调度 API Key 账号，不扫描其他租户、不猜测模型名。
func visionFallbackCandidates(accounts []Account, cfg *config.Config, group *Group) []visionFallbackCandidate {
	return visionFallbackCandidatesWithPolicy(accounts, DefaultVisionFallbackPolicy(cfg), group, nil)
}

// 图片生成/编辑模型能把图片当输入，但输出的是图片而不是可读描述，不能充当视觉助手。
// 名称只是廉价过滤，真正的能力判定仍由目录与官方规则给出。
func visionDescriptorCandidate(account *Account, model string) bool {
	if account == nil || model == "" {
		return false
	}
	if isCodexDedicatedMediaModel(model) || IsGPTImageGenerationModel(model) || isGrokImageGenerationModel(model) {
		return false
	}
	return account.IsModelSupported(model) && account.IsSchedulableForModel(model) && accountHasNativeVision(account, model)
}

func visionFallbackCandidatesWithPolicy(accounts []Account, policy *VisionFallbackPolicy, group *Group, aliases *ModelAliasPolicy) []visionFallbackCandidate {
	if !policy.Enabled {
		return nil
	}
	order := make(map[string]int, len(policy.Models))
	for i, model := range aliases.CanonicalIDs(policy.Models) {
		order[model] = i
	}
	rank := func(model string) int {
		if index, ok := order[aliases.Canonicalize(model)]; ok {
			return index
		}
		return len(order)
	}
	var candidates []visionFallbackCandidate
	for i := range accounts {
		account := &accounts[i]
		if account.Type != AccountTypeAPIKey || !visionFallbackPlatform(account.Platform) ||
			account.IsAnthropicProtocol() || !account.IsSchedulable() ||
			(group != nil && group.RequirePrivacySet && !account.IsPrivacySet()) {
			continue
		}
		for model := range visionFallbackModelIDs(account) {
			canonical := aliases.Canonicalize(model)
			if _, listed := order[canonical]; !listed && !policy.AllowUnlistedModels {
				continue
			}
			if group != nil && !group.ModelAllowlist.Allows(model) && !group.ModelAllowlist.Allows(canonical) {
				continue
			}
			if model == "" || strings.Contains(model, "*") {
				continue
			}
			if !visionDescriptorCandidate(account, model) {
				continue
			}
			candidates = append(candidates, visionFallbackCandidate{account: account, model: model})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if rank(candidates[i].model) != rank(candidates[j].model) {
			return rank(candidates[i].model) < rank(candidates[j].model)
		}
		if candidates[i].account.Priority != candidates[j].account.Priority {
			return candidates[i].account.Priority < candidates[j].account.Priority
		}
		if candidates[i].account.ID != candidates[j].account.ID {
			return candidates[i].account.ID < candidates[j].account.ID
		}
		canonicalI, canonicalJ := aliases.Canonicalize(candidates[i].model), aliases.Canonicalize(candidates[j].model)
		if canonicalI != canonicalJ {
			return canonicalI < canonicalJ
		}
		ids := aliases.IDs(canonicalI)
		return slices.Index(ids, candidates[i].model) < slices.Index(ids, candidates[j].model)
	})
	// 同一账号的等价别名或同一上游目标只尝试一次，保留真实 ID 供转发、日志和缓存使用。
	seenModels := make(map[visionHelperID]bool)
	seenTargets := make(map[visionHelperID]bool)
	return slices.DeleteFunc(candidates, func(candidate visionFallbackCandidate) bool {
		modelID := visionHelperID{accountID: candidate.account.ID, model: aliases.Canonicalize(candidate.model)}
		_, upstream := resolveOpenAIForwardMappedModels(candidate.account, candidate.model, false)
		targetID := visionHelperID{accountID: candidate.account.ID, model: upstream}
		if seenModels[modelID] || seenTargets[targetID] {
			return true
		}
		seenModels[modelID], seenTargets[targetID] = true, true
		return false
	})
}

// 合并公开别名与已同步的具体模型，允许没有显式映射的账号提供已知视觉模型。
func visionFallbackModelIDs(account *Account) map[string]struct{} {
	models := make(map[string]struct{})
	for model := range account.GetModelMapping() {
		models[model] = struct{}{}
	}
	if snapshot := account.GetUpstreamSupportedModelsSnapshot(); snapshot != nil {
		for _, model := range snapshot.Models {
			models[model] = struct{}{}
		}
	}
	if snapshot := account.GetUpstreamModelMetadataSnapshot(); snapshot != nil {
		for model := range snapshot.Models {
			models[model] = struct{}{}
		}
	}
	return models
}

func groupVisionAccounts(accounts []Account, platform, model string) []*Account {
	explicitClaims := false
	for i := range accounts {
		if openAIAccountMatchesPlatform(&accounts[i], platform) && accounts[i].IsSchedulable() && codexExplicitModelMappingClaims(accounts[i], model) {
			explicitClaims = true
			break
		}
	}
	var eligible []*Account
	for i := range accounts {
		account := &accounts[i]
		if !openAIAccountMatchesPlatform(account, platform) || !account.IsSchedulable() || !account.IsModelSupported(model) ||
			!account.IsSchedulableForModel(model) ||
			(explicitClaims && !codexExplicitModelMappingClaims(*account, model)) {
			continue
		}
		eligible = append(eligible, account)
	}
	return eligible
}

// 目录恒声明 text+image：视觉助手覆盖所有可路由模型，图片要么由上游原生处理，
// 要么由视觉助手转成描述。上游透传目录里可能仍写着 text-only，这里统一补齐，
// 客户端不应再因为上游能力快照而禁用发图。
func applyConfiguredVisionFallbackManifest(ctx context.Context, settings *SettingService, cfg *config.Config, body []byte) ([]byte, error) {
	// 保留策略与别名读取：配置损坏时目录端点仍应报错，而不是静默发出目录。
	if _, err := loadVisionFallbackPolicy(ctx, settings, cfg); err != nil {
		return nil, err
	}
	if _, err := settings.GetModelAliasPolicy(ctx); err != nil {
		return nil, err
	}
	return declareCodexVisionInputModalities(body)
}

// declareCodexVisionInputModalities 把所有目录条目的 input_modalities 补成
// text+image，保留其它已有模态（如 audio），并原样保留 original 细节声明。
func declareCodexVisionInputModalities(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, err
	}
	changed := false
	for _, model := range models {
		var modalities []string
		if raw := model["input_modalities"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &modalities); err != nil {
				return nil, err
			}
		}
		declared := []string{"text", "image"}
		for _, modality := range modalities {
			if modality != "text" && modality != "image" {
				declared = append(declared, modality)
			}
		}
		encoded, err := json.Marshal(declared)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(model["input_modalities"]), encoded) {
			continue
		}
		model["input_modalities"] = encoded
		changed = true
	}
	if !changed {
		return body, nil
	}
	encodedModels, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}
	envelope["models"] = encodedModels
	return json.Marshal(envelope)
}
