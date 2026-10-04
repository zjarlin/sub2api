package service

import (
	"context"
	"sort"
	"strings"
)

type autoModelAccountsKey struct{}

type autoModelInventory struct {
	groupID            int64
	accounts           []Account
	models             []string
	platforms          map[string][]string
	decisionOnlyModels map[string]bool
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
	decisionOnlyModels := make(map[string]bool)
	add := func(model, platform string) {
		model = strings.TrimSpace(model)
		if model != "" && !strings.Contains(model, "*") {
			canonical := ModelAliasesFromContext(ctx).Canonicalize(model)
			if IsSystemOneDecisionPlatform(platform) && !AutoModelInventoryDecisionSource(platform, canonical) {
				decisionOnlyModels[canonical] = true
				return
			}
			models[model] = struct{}{}
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
	snapshot := &autoModelInventory{groupID: groupID, accounts: accounts, models: ids, platforms: make(map[string][]string), decisionOnlyModels: decisionOnlyModels}
	for model, values := range platforms {
		delete(decisionOnlyModels, model)
		for platform := range values {
			snapshot.platforms[model] = append(snapshot.platforms[model], platform)
		}
		sort.Strings(snapshot.platforms[model])
	}
	return context.WithValue(ctx, autoModelAccountsKey{}, snapshot), ids, nil
}

// 决策平台只解释自己的决策模型，不能把共享 /models 目录当作文本推理能力。
func AutoModelInventoryDecisionSource(platform, model string) bool {
	switch platform {
	case PlatformJev:
		return model == DefaultJevModel
	case PlatformLaya:
		return model == DefaultLayaModel || strings.HasPrefix(model, DefaultLayaModel+"-")
	default:
		return false
	}
}

// 只过滤完全来自决策目录的伪候选；同名模型的真实文本平台来源仍然保留。
func AutoModelInventoryDecisionOnlyModel(ctx context.Context, model string) bool {
	if snapshot, ok := ctx.Value(autoModelAccountsKey{}).(*autoModelInventory); ok {
		return snapshot.decisionOnlyModels[model]
	}
	return false
}

// 保留平台候选，即使该平台账号暂时离线，供路由计划解释排除原因。
func AutoModelInventoryPlatforms(ctx context.Context, model string) []string {
	if snapshot, ok := ctx.Value(autoModelAccountsKey{}).(*autoModelInventory); ok {
		return snapshot.platforms[model]
	}
	return nil
}

// Auto 选模先按已配置能力档位从高到低，同一档位内再按用户指定的性价比顺序：
// deepseek-v4.1-flash 固定为全池首选，其后为 GLM 系列，再其他 DeepSeek、Kimi、
// MiniMax、Mimo、Qwen3、Step 系列，最后其余文本模型。未评级模型排在所有已评级模型之后。
// 品牌加成只在同一档位内生效，避免低档模型（如通用档的 glm-5.2）反超更高档的可用模型。
func AutoModelPriority(ctx context.Context, model string) (int, int) {
	canonical := ModelAliasesFromContext(ctx).Canonicalize(model)
	name := strings.ToLower(canonical)
	name = name[strings.LastIndex(name, "/")+1:]

	// 其余模型以能力档位为主键，未评级模型用兜底档位排在所有已评级模型之后。
	tier := 1000000
	if snapshot, ok := ctx.Value(autoModelRoutingPolicyContextKey{}).(*autoModelRoutingPolicy); ok {
		if rank, found := snapshot.ranks[canonical]; found {
			tier = rank
		}
	}
	// 首选模型无视档位固定排在最前；仍保留 tier 让规范名先于带前缀的重复项。
	if name == "deepseek-v4.1-flash" {
		return 0, tier
	}
	family := 3
	switch {
	case strings.HasPrefix(name, "glm-"):
		family = 1
	case strings.HasPrefix(name, "deepseek-"), strings.HasPrefix(name, "kimi-"),
		strings.HasPrefix(name, "minimax-"), strings.HasPrefix(name, "mimo-"),
		strings.HasPrefix(name, "qwen3"), strings.HasPrefix(name, "step-"):
		family = 2
	}
	return tier + 1, family
}
