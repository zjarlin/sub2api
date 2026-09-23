package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// DiagnoseModelAvailabilityForPlatform 检查指定平台的分组中是否配置过请求模型。
// 平台条件防止不同 OpenAI 兼容平台互相污染；查询绕过调度快照，并保留
// 因上游错误而退出调度的账号。
//
// Safe to call on the error path: returns {true,true} on any internal
// failure or when the inputs preclude meaningful diagnosis (empty model,
// nil service), so callers stay on the 503 fallback branch.
func (s *OpenAIGatewayService) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) ModelAvailabilityDiagnosis {
	if s == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	if s.accountRepo == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	platform = NormalizeOpenAICompatiblePlatform(platform)
	// 混合调度：目标平台分组可纳入启用 mixed_scheduling 的来源平台账号
	// （例如 openai/Codex 分组可由 traework/workbuddy 账号服务）。
	useMixed := len(MixedSchedulingSourcePlatforms(platform)) > 0
	platforms := []string{platform}
	if useMixed {
		platforms = append(platforms, MixedSchedulingSourcePlatforms(platform)...)
	}
	queryGroupID := groupID
	includeGrouped := false
	if useMixed {
		// 与调度器一致的取号范围：显式分组优先，无分组 simple 模式扫描全部账号。
		if groupID == nil && s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
			includeGrouped = true
		}
	} else if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(
		ctx,
		queryGroupID,
		platforms,
		includeGrouped,
	)
	if err != nil {
		// Conservative fallback so the caller keeps returning 503; we do not
		// want a transient lookup failure to flip into 404 model_not_found.
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	for i := range accounts {
		if useMixed && accounts[i].Platform != platform &&
			!(accounts[i].IsMixedSchedulingEnabled() && mixedSchedulingTargetsPlatform(accounts[i].Platform, platform)) {
			continue
		}
		diag.HasAccountsInPool = true
		// 与调度使用同一模型资格判断，缺失目录和配置不能被诊断为支持全部模型。
		if accounts[i].IsModelSupported(requestedModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}
