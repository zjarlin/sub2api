package service

import (
	"context"
	"encoding/json"
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
	return accountCodexModelSupportsImageInput(account, upstream)
}

func accountHasKnownTextOnlyInput(account *Account, model string) bool {
	_, upstream := resolveOpenAIForwardMappedModels(account, model, false)
	if metadata, ok := account.GetUpstreamModelMetadata(upstream); ok && len(metadata.InputModalities) > 0 {
		return !stringSliceContains(normalizeCodexInputModalities(metadata.InputModalities), "image")
	}
	// WorkBuddy 的地域前缀只参与路由，不能掩盖纯文本模型的能力限制。
	if realm, bareModel, ok := strings.Cut(upstream, ":"); account.IsWorkbuddy() && ok && (realm == "cn" || realm == "global") {
		upstream = bareModel
	}
	return isDeepSeekCodexModel(upstream)
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
	return visionFallbackCandidatesWithPolicy(accounts, DefaultVisionFallbackPolicy(cfg), group)
}

func visionFallbackCandidatesWithPolicy(accounts []Account, policy *VisionFallbackPolicy, group *Group) []visionFallbackCandidate {
	if !policy.Enabled {
		return nil
	}
	order := make(map[string]int, len(policy.Models))
	for i, model := range policy.Models {
		order[model] = i
	}
	rank := func(model string) int {
		if index, ok := order[model]; ok {
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
			if _, listed := order[model]; !listed && !policy.AllowUnlistedModels {
				continue
			}
			if group != nil && !group.ModelAllowlist.Allows(model) {
				continue
			}
			if model == "" || strings.Contains(model, "*") {
				continue
			}
			if isCodexDedicatedMediaModel(model) || !account.IsModelSupported(model) || !account.IsSchedulableForModel(model) || !accountHasNativeVision(account, model) {
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
		return candidates[i].model < candidates[j].model
	})
	return candidates
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
		if accounts[i].Platform == platform && accounts[i].IsSchedulable() && codexExplicitModelMappingClaims(accounts[i], model) {
			explicitClaims = true
			break
		}
	}
	var eligible []*Account
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform || !account.IsSchedulable() || !account.IsModelSupported(model) ||
			!account.IsSchedulableForModel(model) ||
			(explicitClaims && !codexExplicitModelMappingClaims(*account, model)) {
			continue
		}
		eligible = append(eligible, account)
	}
	return eligible
}

func groupModelHasNativeVision(accounts []Account, platform, model string) bool {
	// 已验证的型号能力不随账号临时冷却或新账号缺少快照而消失。
	verifiedModels := make(map[string]bool)
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform || account.Status != StatusActive || !account.IsModelSupported(model) {
			continue
		}
		verified := account.GetUpstreamModelMetadataSnapshot()
		if verified == nil || verified.Source != "verified-responses-image" || !accountHasNativeVision(account, model) {
			continue
		}
		_, upstream := resolveOpenAIForwardMappedModels(account, model, false)
		verifiedModels[upstream] = true
	}
	if len(verifiedModels) == 0 {
		return false
	}
	eligible := groupVisionAccounts(accounts, platform, model)
	if len(eligible) == 0 {
		return false
	}
	for _, account := range eligible {
		if accountHasNativeVision(account, model) {
			continue
		}
		// 只让同一最终 GPT 型号继承已验证能力，明确的纯文本声明始终优先。
		_, upstream := resolveOpenAIForwardMappedModels(account, model, false)
		if accountHasKnownTextOnlyInput(account, model) || !isGPTSeriesModel(upstream) || !verifiedModels[upstream] {
			return false
		}
	}
	return true
}

// 目录与转发共用辅助策略，只有所有可选账号都能处理图片时才声明图像输入。
func groupModelNeedsVisionFallback(accounts []Account, platform, model string) bool {
	knownText := false
	for _, account := range groupVisionAccounts(accounts, platform, model) {
		if accountNeedsVisionFallback(account, model) {
			knownText = true
			continue
		}
		if !accountHasNativeVision(account, model) {
			return false
		}
	}
	return knownText
}

// 使用当前可调度账号的能力更新目录，避免暂不可用的旧账号隐藏原生视觉。
func applyVisionFallbackManifest(body []byte, cfg *config.Config, group *Group, platform string, accounts []Account, routes []CompositeModelRoute, routesAvailable bool) ([]byte, error) {
	return applyVisionFallbackManifestWithPolicy(body, DefaultVisionFallbackPolicy(cfg), group, platform, accounts, routes, routesAvailable)
}

func applyConfiguredVisionFallbackManifest(ctx context.Context, settings *SettingService, body []byte, cfg *config.Config, group *Group, platform string, accounts []Account, routes []CompositeModelRoute, routesAvailable bool) ([]byte, error) {
	policy, err := loadVisionFallbackPolicy(ctx, settings, cfg)
	if err != nil {
		return nil, err
	}
	return applyVisionFallbackManifestWithPolicy(body, policy, group, platform, accounts, routes, routesAvailable)
}

func applyVisionFallbackManifestWithPolicy(body []byte, policy *VisionFallbackPolicy, group *Group, platform string, accounts []Account, routes []CompositeModelRoute, routesAvailable bool) ([]byte, error) {
	if len(accounts) == 0 {
		return body, nil
	}
	helperAvailable := len(visionFallbackCandidatesWithPolicy(accounts, policy, group)) > 0
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
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			return nil, err
		}
		target := platform
		targetModel := slug
		if target == PlatformComposite {
			var resolved bool
			target, targetModel, resolved = resolveCodexCompositeModelTarget(slug, accounts, routes, routesAvailable)
			if !resolved {
				continue
			}
		}
		if !visionFallbackPlatform(target) {
			continue
		}
		nativeVision := groupModelHasNativeVision(accounts, target, targetModel)
		assistedVision := helperAvailable && groupModelNeedsVisionFallback(accounts, target, targetModel)
		if !nativeVision && !assistedVision {
			continue
		}
		var modalities []string
		if raw := model["input_modalities"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &modalities); err != nil {
				return nil, err
			}
		}
		if stringSliceContains(modalities, "image") {
			continue
		}
		model["input_modalities"] = json.RawMessage(`["text","image"]`)
		if assistedVision {
			// 辅助模型使用标准 high 细节，不宣称原模型支持 original 像素模式。
			model["supports_image_detail_original"] = json.RawMessage(`false`)
		}
		changed = true
	}
	if !changed {
		return body, nil
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}
	envelope["models"] = encoded
	return json.Marshal(envelope)
}
