package handler

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const autoModelPlanKey = "auto_model_plan"

// free 入口使用独立的候选集合，确保后续重试、降级和路由观测都只包含免费模型。
func (h *GatewayHandler) autoModelPlan(ctx context.Context, group *service.Group, resolver *service.CompositeRouteResolver, path string, body []byte, models []string) ([]autoModelRouteCandidate, []service.AutoModelCandidate, error) {
	explicit, err := resolver.AutoModelRoutes(ctx, group.ID)
	if err != nil {
		return nil, nil, err
	}
	var discovered []string
	if group.Platform == service.PlatformComposite {
		discovered = h.compositeAvailableModels(ctx, &group.ID)
	} else {
		discovered = h.gatewayService.GetAvailableModels(ctx, &group.ID, group.Platform)
		if discovered == nil {
			discovered = defaultCodexModelIDsForPlatform(group.Platform)
		}
	}
	models = mergeModelIDs(models, discovered)
	aliases := service.ModelAliasesFromContext(ctx)
	if group.Platform == service.PlatformComposite {
		for _, rule := range explicit {
			if rule.MatchType == service.CompositeRouteMatchExact {
				models = append(models, rule.PublicModel)
			}
		}
	}
	isFree := service.AutoModelRequestIsFree(ctx)
	if isFree {
		models = slices.DeleteFunc(models, func(model string) bool {
			return !service.AutoModelFreeModelID(model)
		})
	}
	aliasesByModel := make(map[string][]string)
	for _, id := range models {
		canonical := aliases.Canonicalize(id)
		if canonical != id {
			aliasesByModel[canonical] = append(aliasesByModel[canonical], id)
		}
	}
	canonical := aliases.CanonicalIDs(models)
	domain := autoModelRequestDomain(body)
	isAsk := service.AutoModelRequestIsAsk(ctx)

	sort.SliceStable(canonical, func(i, j int) bool {
		di, domainI := autoModelDomainPriority(domain, canonical[i])
		dj, domainJ := autoModelDomainPriority(domain, canonical[j])
		if isAsk {
			// ask 优先免费模型，其次 web 问答适配器，再其他问答模型；同组内沿用性价比顺序。
			ai := autoModelAskSortKey(canonical[i], platformOfModel(ctx, canonical[i]))
			bi := autoModelAskSortKey(canonical[j], platformOfModel(ctx, canonical[j]))
			if ai != bi {
				return ai < bi
			}
		} else if domainI != domainJ {
			return domainI
		} else if domainI && di != dj {
			return di < dj
		}
		pi, ti := service.AutoModelPriority(ctx, canonical[i])
		pj, tj := service.AutoModelPriority(ctx, canonical[j])
		if pi != pj {
			return pi < pj
		}
		if ti != tj {
			return ti < tj
		}
		return canonical[i] < canonical[j]
	})
	routes := make([]autoModelRouteCandidate, 0, len(canonical))
	plan := make([]service.AutoModelCandidate, 0, len(canonical))
	for _, model := range canonical {
		// ask 链路排除 deepseek-v4.1-flash，避免问答被该模型抢占；auto 链路不受影响。
		if isAsk && service.ModelAliasesFromContext(ctx).Canonicalize(model) == "deepseek-v4.1-flash" {
			continue
		}
		platforms := service.AutoModelInventoryPlatforms(ctx, model)
		upstream := model
		if rule, matched := service.MatchAutoModelRoute(explicit, model, autoModelEndpoint(path)); matched && group.Platform == service.PlatformComposite {
			platforms = []string{rule.TargetPlatform}
			if strings.TrimSpace(rule.UpstreamModel) != "" {
				upstream = rule.UpstreamModel
			}
		}
		if len(platforms) == 0 {
			// A decision-platform model in the display catalog must not become a text candidate without a source.
			if service.AutoModelInventoryDecisionOnlyModel(ctx, model) {
				continue
			}
			platforms = []string{""}
		}
		for _, platform := range platforms {
			// An explicit composite rule also cannot point a text model at a decision-only platform.
			if service.IsSystemOneDecisionPlatform(platform) && !service.AutoModelInventoryDecisionSource(platform, model) {
				continue
			}
			if isFree && !service.AutoModelFreeModelID(model) && !service.AutoModelFreeModelID(upstream) {
				continue
			}
			entry := service.AutoModelCandidate{Model: model, Platform: platform, Aliases: aliasesByModel[model]}
			switch {
			case platform == "":
				entry.Reason = "source_missing"
			case !autoModelTextCandidate(model) || !autoModelTextCandidate(upstream):
				entry.Reason = "not_text_generation"
			case service.AutoModelPolicyExcluded(ctx, model, upstream):
				entry.Reason = "auto_policy_excluded"
			case !service.AutoModelWhitelisted(ctx, model) || !service.AutoModelWhitelisted(ctx, upstream):
				entry.Reason = "auto_policy_not_whitelisted"
			case !h.autoModelTargetAllowed(ctx, group.ID, model) || !h.autoModelTargetAllowed(ctx, group.ID, upstream):
				entry.Reason = "auto_policy_excluded"
			case !autoModelTextPlatform(platform):
				entry.Reason = "protocol_not_supported"
			case !service.AutoModelPlatformAllowed(ctx, platform):
				entry.Reason = "ask_only_platform"
			default:
				compatible, err := h.gatewayService.AutoModelAccountCompatible(ctx, &group.ID, platform, upstream, body)
				if err != nil {
					return nil, nil, err
				}
				entry.Eligible = compatible
				if !compatible {
					entry.Reason = "no_compatible_account"
				}
			}
			if entry.Eligible {
				entry.Order = len(routes) + 1
				routes = append(routes, autoModelRouteCandidate{model: model, targetPlatform: platform, upstreamModel: upstream})
			}
			plan = append(plan, entry)
		}
	}
	return routes, plan, nil
}

// autoModelAskSortKey 计算 ask 虚拟模型组内候选的优先级键，越小越靠前。
// 第一档：免费模型；第二档：web 问答适配器（Ask 专属平台）；第三档：其他问答模型。
// 仅在 ask 链路使用，auto 排序不受影响。
func autoModelAskSortKey(model, platform string) int {
	if service.AutoModelFreeModelID(model) {
		return 0
	}
	if service.AutoModelWebAskPlatform(platform) {
		return 1
	}
	return 2
}

// platformOfModel 返回模型的可调度平台，用于 ask 排序判定 web 问答适配器。
// 优先使用 Auto 清单里登记的来源平台；没有则回退按模型名探测平台。
func platformOfModel(ctx context.Context, model string) string {
	if platforms := service.AutoModelInventoryPlatforms(ctx, model); len(platforms) > 0 {
		return platforms[0]
	}
	if platform, ok := service.DetectModelPlatform(model); ok {
		return platform
	}
	return ""
}
