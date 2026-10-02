package service

import (
	"context"
	"sort"
	"strings"
)

// 模型发现读取已配置账号，保留限流、余额错误等运行时状态下的目录。
// 仓储负责分组隔离与排除已删除、已禁用和非公开账号；健康记录只供调度与探测使用。
func loadModelCatalogAccounts(ctx context.Context, repo AccountRepository, groupID *int64, platform string) ([]Account, error) {
	if repo == nil {
		return nil, nil
	}
	accounts, err := repo.ListModelAvailabilityCandidates(ctx, groupID, modelCatalogCandidatePlatforms(platform), groupID == nil)
	if err != nil {
		return nil, err
	}
	return filterModelCatalogAccountsForPlatform(accounts, platform), nil
}

func modelCatalogCandidatePlatforms(targetPlatform string) []string {
	targetPlatform = strings.TrimSpace(targetPlatform)
	if targetPlatform == PlatformComposite || targetPlatform == "" {
		return []string{
			PlatformAnthropic,
			PlatformOpenAI,
			PlatformGemini,
			PlatformAntigravity,
			PlatformGrok,
			PlatformKimi,
			PlatformZhipu,
			PlatformDeepseek,
			PlatformDeepseekWeb,
			PlatformMiniMax,
			PlatformCursor,
			PlatformWindsurf,
			PlatformOpenCodeGo,
			PlatformDoubao,
			PlatformTraework,
			PlatformWorkbuddy,
			PlatformVibex,
			PlatformArena,
			PlatformZcode,
			PlatformQoder,
			PlatformLaya,
			PlatformJev,
		}
	}
	return append([]string{targetPlatform}, MixedSchedulingSourcePlatforms(targetPlatform)...)
}

func filterModelCatalogAccountsForPlatform(accounts []Account, targetPlatform string) []Account {
	targetPlatform = strings.TrimSpace(targetPlatform)
	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].Status == StatusDisabled || !accounts[i].IsPubliclyShared() {
			continue
		}
		if targetPlatform == PlatformComposite || targetPlatform == "" {
			if isConcreteRequestPlatform(accounts[i].Platform) {
				filtered = append(filtered, accounts[i])
			}
			continue
		}
		if openAIAccountMatchesPlatform(&accounts[i], targetPlatform) {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}

// 目录和 Auto 计划共用投影规则；默认模型只用于展示，不证明存在可调度来源。
func availableModelsFromAccounts(accounts []Account, platform string) []string {
	if len(accounts) == 0 {
		return []string{}
	}
	modelSet := make(map[string]struct{})
	hasAnyCatalog := false
	for i := range accounts {
		account := &accounts[i]
		mapping := account.GetModelMapping()
		if len(mapping) > 0 {
			hasAnyCatalog = true
			for model := range mapping {
				if model = strings.TrimSpace(model); model != "" {
					modelSet[model] = struct{}{}
				}
			}
			continue
		}
		if snapshot := account.GetUpstreamSupportedModelsSnapshot(); snapshot != nil {
			hasAnyCatalog = true
			for _, model := range snapshot.Models {
				if model = strings.TrimSpace(model); model != "" {
					modelSet[model] = struct{}{}
				}
			}
		}
	}
	// 没有账号映射或已同步目录时返回 nil，由调用方使用平台默认模型。
	if !hasAnyCatalog {
		return nil
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	if platform == PlatformOpenAI {
		models = supplementUnmappedOpenAIModels(accounts, models)
	}
	return models
}
