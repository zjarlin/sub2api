package handler

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const autoModelPlanKey = "auto_model_plan"

// All configured models enter the plan; eligible models are ordered by cost and capability.
func (h *GatewayHandler) autoModelPlan(ctx context.Context, group *service.Group, resolver *service.CompositeRouteResolver, path string, body []byte, models []string) ([]autoModelRouteCandidate, []service.AutoModelCandidate, error) {
	explicit, err := resolver.AutoModelRoutes(ctx, group.ID)
	if err != nil {
		return nil, nil, err
	}
	// The display catalog and real sources enter metadata together; defaults without a source explain exclusion only.
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
	domain := autoModelRequestDomain(body)
	sort.SliceStable(canonical, func(i, j int) bool {
		di, domainI := autoModelDomainPriority(domain, canonical[i])
		dj, domainJ := autoModelDomainPriority(domain, canonical[j])
		if domainI != domainJ {
			return domainI
		}
		if domainI && di != dj {
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
