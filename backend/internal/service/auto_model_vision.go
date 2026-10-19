package service

import (
	"context"

	"github.com/tidwall/gjson"
)

func autoModelInputHasImages(value gjson.Result) bool {
	if !value.IsObject() && !value.IsArray() {
		return false
	}
	switch value.Get("type").String() {
	case "input_image", "image_url", "image":
		return true
	}
	found := false
	value.ForEach(func(_, child gjson.Result) bool {
		found = autoModelInputHasImages(child)
		return !found
	})
	return found
}

// 只在图片请求中读取辅助策略，复用本次分组账号快照并遵守助手白名单。
func (s *GatewayService) BindAutoModelVisionCapabilities(ctx context.Context, group *Group) (context.Context, error) {
	capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
	if !capabilities.images || group == nil {
		return ctx, nil
	}
	policy, err := loadVisionFallbackPolicy(ctx, s.settingService, s.cfg)
	if err != nil {
		return ctx, err
	}
	snapshot, ok := ctx.Value(autoModelAccountsKey{}).(*autoModelInventory)
	if !ok || snapshot.groupID != group.ID {
		return ctx, nil
	}
	for _, helper := range visionFallbackCandidatesWithPolicy(snapshot.accounts, policy, group, ModelAliasesFromContext(ctx)) {
		if isOpenAICompatibleAccountEligibleForRequestBeforeProfit(ctx, helper.account, helper.account.Platform, helper.model, false, "") {
			capabilities.visionFallback = true
			break
		}
	}
	return context.WithValue(ctx, autoModelRequestCapabilitiesContextKey{}, capabilities), nil
}

// 预检选中型号之后，实际选号和粘性会话重检也必须满足同一图片能力。
func autoModelAccountSupportsImages(ctx context.Context, account *Account, model string) bool {
	if ctx == nil {
		return true
	}
	capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
	if IsAutoModelRouting(ctx) && !AutoModelPlatformAllowed(ctx, account.Platform) {
		return false
	}
	return !capabilities.images || accountHasNativeVision(account, model) ||
		(capabilities.visionFallback && accountNeedsVisionFallback(account, model))
}

// Auto 的预检和取得账号槽位后的复检共用辅助能力，保留其余上下文和工具限制。
func AutoModelRequestAccountCompatible(ctx context.Context, account *Account, model string, body []byte) bool {
	if account == nil || (IsAutoModelRouting(ctx) && !AutoModelPlatformAllowed(ctx, account.Platform)) {
		return false
	}
	capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
	assistedVision := capabilities.visionFallback && accountNeedsVisionFallback(account, model)
	if IsAutoModelRouting(ctx) && modelRequestNeedsNativeSearchTools(body) && !configuredAccountHasNativeSearch(ctx, account, model, body) {
		// 托管搜索只是可选工具：没有原生能力或辅助助手时丢弃该声明，继续用同一账号
		// 服务其余文本与函数工具。否则 Codex 默认附带的 web_search 会让 Auto 预检把
		// 全部候选判成 no_compatible_account，整轮请求直接 503（2026-10-19 事故）。
		var err error
		body, err = stripDelegatedSearchTools(body)
		if err != nil {
			return false
		}
	}
	// Auto 请求的账号级预检把「服务端引用型条目」交给真实转发路径规范化/丢弃，
	// 与辅助视觉一样按能力放宽；换模型重放仍走严格判定。
	return modelAccountCompatibleWithOptions(account, model, body, assistedVision, true)
}
