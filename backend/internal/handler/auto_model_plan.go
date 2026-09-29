package handler

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const autoModelPlanKey = "auto_model_plan"

// 所有已配置模型都进入计划，能执行的模型按性价比和能力排序；账号名和凭据不对客户端公开。
func (h *GatewayHandler) autoModelPlan(ctx context.Context, group *service.Group, resolver *service.CompositeRouteResolver, path string, body []byte, models []string) ([]autoModelRouteCandidate, []service.AutoModelCandidate, error) {
	explicit, err := resolver.AutoModelRoutes(ctx, group.ID)
	if err != nil {
		return nil, nil, err
	}
	// 展示目录与实际来源一起进入元数据；没有来源的默认模型只解释排除原因。
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
	aliasesByModel := make(map[string][]string)
	for _, id := range models {
		canonical := aliases.Canonicalize(id)
		if canonical != id {
			aliasesByModel[canonical] = append(aliasesByModel[canonical], id)
		}
	}
	canonical := aliases.CanonicalIDs(models)
	sort.SliceStable(canonical, func(i, j int) bool {
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
		platforms := service.AutoModelInventoryPlatforms(ctx, model)
		upstream := model
		if rule, matched := service.MatchAutoModelRoute(explicit, model, autoModelEndpoint(path)); matched && group.Platform == service.PlatformComposite {
			platforms = []string{rule.TargetPlatform}
			if strings.TrimSpace(rule.UpstreamModel) != "" {
				upstream = rule.UpstreamModel
			}
		}
		if len(platforms) == 0 {
			// 目录里的决策平台模型不能通过展示补全变成无来源文本候选。
			if service.AutoModelInventoryDecisionOnlyModel(ctx, model) {
				continue
			}
			platforms = []string{""}
		}
		for _, platform := range platforms {
			// 显式合成规则同样不能把文本模型指向仅提供决策协议的平台。
			if service.IsSystemOneDecisionPlatform(platform) && !service.AutoModelInventoryDecisionSource(platform, model) {
				continue
			}
			entry := service.AutoModelCandidate{Model: model, Platform: platform, Aliases: aliasesByModel[model]}
			switch {
			case platform == "":
				entry.Reason = "source_missing"
			case !autoModelTextCandidate(model) || !autoModelTextCandidate(upstream):
				entry.Reason = "not_text_generation"
			case !h.autoModelTargetAllowed(ctx, group.ID, model) || !h.autoModelTargetAllowed(ctx, group.ID, upstream):
				entry.Reason = "auto_policy_excluded"
			case !autoModelTextPlatform(platform):
				entry.Reason = "protocol_not_supported"
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
