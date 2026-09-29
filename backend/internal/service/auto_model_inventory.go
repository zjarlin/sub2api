package service

import (
	"context"
	"sort"
	"strings"
)

type autoModelAccountsKey struct{}

type autoModelInventory struct {
	groupID   int64
	accounts  []Account
	models    []string
	platforms map[string][]string
}

// 每次请求读取一次完整分组账号，包括暂不可调度账号；不依赖成功调用记录。
func (s *GatewayService) BindAutoModelInventory(ctx context.Context, groupID int64) (context.Context, []string, error) {
	if snapshot, ok := ctx.Value(autoModelAccountsKey{}).(*autoModelInventory); ok && snapshot.groupID == groupID {
		return ctx, snapshot.models, nil
	}
	accounts, err := loadModelCatalogAccounts(ctx, s.accountRepo, &groupID, PlatformComposite)
	if err != nil {
		return ctx, nil, err
	}
	accounts = accountsWithModelAliases(ctx, accounts)
	models := make(map[string]struct{})
	platforms := make(map[string]map[string]struct{})
	add := func(model, platform string) {
		model = strings.TrimSpace(model)
		if model != "" && !strings.Contains(model, "*") {
			models[model] = struct{}{}
			canonical := ModelAliasesFromContext(ctx).Canonicalize(model)
			if platforms[canonical] == nil {
				platforms[canonical] = make(map[string]struct{})
			}
			platforms[canonical][platform] = struct{}{}
		}
	}
	for i := range accounts {
		account := &accounts[i]
		for model := range account.GetModelMapping() {
			add(model, account.Platform)
		}
		if snapshot := account.GetUpstreamSupportedModelsSnapshot(); snapshot != nil {
			for _, model := range snapshot.Models {
				add(model, account.Platform)
			}
		}
	}
	ids := make([]string, 0, len(models))
	for model := range models {
		ids = append(ids, model)
	}
	sort.Strings(ids)
	snapshot := &autoModelInventory{groupID: groupID, accounts: accounts, models: ids, platforms: make(map[string][]string)}
	for model, values := range platforms {
		for platform := range values {
			snapshot.platforms[model] = append(snapshot.platforms[model], platform)
		}
		sort.Strings(snapshot.platforms[model])
	}
	return context.WithValue(ctx, autoModelAccountsKey{}, snapshot), ids, nil
}

// 保留平台候选，即使该平台账号暂时离线，供路由计划解释排除原因。
func AutoModelInventoryPlatforms(ctx context.Context, model string) []string {
	if snapshot, ok := ctx.Value(autoModelAccountsKey{}).(*autoModelInventory); ok {
		return snapshot.platforms[model]
	}
	return nil
}

// 用户指定的性价比顺序优先，其余模型按既有能力档位排序，未评级模型仍保留。
func AutoModelPriority(ctx context.Context, model string) (int, int) {
	canonical := ModelAliasesFromContext(ctx).Canonicalize(model)
	name := strings.ToLower(canonical)
	name = name[strings.LastIndex(name, "/")+1:]
	priority := 3
	switch {
	case name == "deepseek-v4.1-flash":
		priority = 0
	case strings.HasPrefix(name, "glm-"):
		priority = 1
	case strings.HasPrefix(name, "deepseek-"), strings.HasPrefix(name, "kimi-"),
		strings.HasPrefix(name, "minimax-"), strings.HasPrefix(name, "mimo-"),
		strings.HasPrefix(name, "qwen3"), strings.HasPrefix(name, "step-"):
		priority = 2
	}
	tier := 1000000
	if snapshot, ok := ctx.Value(autoModelRoutingPolicyContextKey{}).(*autoModelRoutingPolicy); ok {
		if rank, found := snapshot.ranks[canonical]; found {
			tier = rank
		}
	}
	return priority, tier
}
