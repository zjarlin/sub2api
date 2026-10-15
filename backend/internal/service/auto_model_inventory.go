package service

import (
	"context"
	"sort"
	"strconv"
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

// collectAutoModelInventory 从账号目录收集 Auto 可用的真实模型 ID、每个规范模型
// 的来源平台，以及只作决策用的平台模型。档位降级的 auto 展开与本函数共用同一
// 收集规则，保证两处候选集合一致。
func collectAutoModelInventory(ctx context.Context, accounts []Account) (ids []string, platforms map[string][]string, decisionOnlyModels map[string]bool) {
	models := make(map[string]struct{})
	platformSets := make(map[string]map[string]struct{})
	decisionOnlyModels = make(map[string]bool)
	add := func(model, platform string) {
		model = strings.TrimSpace(model)
		if model == "" || strings.Contains(model, "*") {
			return
		}
		canonical := ModelAliasesFromContext(ctx).Canonicalize(model)
		if IsSystemOneDecisionPlatform(platform) && !AutoModelInventoryDecisionSource(platform, canonical) {
			decisionOnlyModels[canonical] = true
			return
		}
		models[model] = struct{}{}
		if platformSets[canonical] == nil {
			platformSets[canonical] = make(map[string]struct{})
		}
		platformSets[canonical][platform] = struct{}{}
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
	ids = make([]string, 0, len(models))
	for model := range models {
		ids = append(ids, model)
	}
	sort.Strings(ids)
	platforms = make(map[string][]string, len(platformSets))
	for model, values := range platformSets {
		delete(decisionOnlyModels, model)
		for platform := range values {
			platforms[model] = append(platforms[model], platform)
		}
		sort.Strings(platforms[model])
	}
	return ids, platforms, decisionOnlyModels
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
	ids, platforms, decisionOnlyModels := collectAutoModelInventory(ctx, accounts)
	snapshot := &autoModelInventory{groupID: groupID, accounts: accounts, models: ids, platforms: platforms, decisionOnlyModels: decisionOnlyModels}
	return context.WithValue(ctx, autoModelAccountsKey{}, snapshot), ids, nil
}

// AutoModelTextCandidate 判断模型是否可以作为文本生成候选参与 Auto 与降级：
// 排除虚拟模型、通配 ID、决策平台模型以及 guard/safety/embedding 等专有模型，
// 避免把文本请求打到只能做单次判定或不产出文本的端点。服务层与 handler 共用同一
// 判定，防止两处漂移。
func AutoModelTextCandidate(model string) bool {
	platform, _ := DetectModelPlatform(model)
	if IsVirtualModelID(model) || strings.Contains(model, "*") || IsSystemOneDecisionPlatform(platform) {
		return false
	}
	name := strings.ToLower(model)
	for _, specialized := range []string{
		"embed", "esmfold", "moderation", "image", "audio", "video", "tts-", "whisper", "transcri", "dall-e",
		"translate", "translation", "safety", "guard", "calibration", "rerank", "re-rank", "classifier", "reward-model", "parser",
		// 决策/分类模型只做单次前向判定，不产出文本；供应商聚合目录常把它
		// 当作普通聊天模型登记，纳入 Auto 只会把文本请求打到不支持的端点。
		"decision-model",
	} {
		if strings.Contains(name, specialized) {
			return false
		}
	}
	return true
}

// 保留的虚拟模型 ID：auto（自动选模）、ask（问答专用）、tier-N（能力档位）。
// 它们由 Auto 中间件解析为真实模型，不能作为真实候选。
const AskModelID = "ask"
const ModelTierVirtualIDPrefix = "tier-"

// AutoModelTextPlatform 报告平台是否可作为文本生成目标；决策平台与专用媒体
// 平台不参与文本选模。Auto 中间件与档位降级的 auto 展开共用同一规则。
func AutoModelTextPlatform(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformGrok, PlatformKimi,
		PlatformZhipu, PlatformDeepseek, PlatformMiniMax,
		PlatformOpenCodeGo, PlatformKilo, PlatformDoubao, PlatformDeepseekWeb, PlatformMadao, PlatformCursor, PlatformWindsurf, PlatformTraework,
		PlatformWorkbuddy, PlatformVibex, PlatformZcode,
		PlatformQoder:
		return true
	default:
		return false
	}
}

// IsVirtualModelID 报告模型是否是保留的虚拟模型 ID（auto/ask/档位）。
func IsVirtualModelID(model string) bool {
	if model == ModelFallbackAutoModel || model == AskModelID {
		return true
	}
	value, ok := strings.CutPrefix(model, ModelTierVirtualIDPrefix)
	if !ok || value == "" || value[0] == '0' {
		return false
	}
	index, err := strconv.Atoi(value)
	return err == nil && index > 0
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

// AutoModelRequestIsAsk 报告当前 Auto 路由请求是否是 ask 虚拟模型组。
// 仅影响问答链路，auto 与其他打包模型组不受影响。
func AutoModelRequestIsAsk(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	capabilities, _ := ctx.Value(autoModelRequestCapabilitiesContextKey{}).(autoModelRequestCapabilities)
	return capabilities.ask
}

// autoModelWebAskPlatform 报告平台是否属于 Ask 专属的 web 问答适配器：
// 这些平台只在 ask 请求中参与调度，常对应聊天/网页问答上游。
func AutoModelWebAskPlatform(platform string) bool {
	switch platform {
	case PlatformDoubao, PlatformDeepseekWeb, PlatformMadao, PlatformCursor, PlatformWindsurf:
		return true
	default:
		return false
	}
}

// autoModelFreeModelID 报告模型 ID 是否属于免费模型：
// 匹配厂商 free 命名空间（kilo-auto/free、vendor/free）、v1 免费变体
// （*:free、*/free），以及开放免费池目录。免费模型在 ask 优先选用。
func AutoModelFreeModelID(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return false
	}
	if strings.HasSuffix(name, ":free") || strings.HasSuffix(name, "/free") || name == "kilo-auto/free" || name == "openrouter/free" {
		return true
	}
	return false
}
