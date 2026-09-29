package service

import (
	"context"
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
		if accounts[i].Status == StatusDisabled {
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
