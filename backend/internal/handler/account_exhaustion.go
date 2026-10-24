package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

// 只把通过原有 Auto 策略的候选绑定到指定账号，不扩大权限或媒体能力。
func prependAccountExhaustionRoutes(ctx context.Context, groupID int64, body []byte, routes []autoModelRouteCandidate) []autoModelRouteCandidate {
	if gjson.GetBytes(body, "previous_response_id").String() != "" || !service.ModelFallbackRequestPortable(body) {
		return routes
	}
	aliases := service.ModelAliasesFromContext(ctx)
	preferred := make([]autoModelRouteCandidate, 0)
	for _, account := range service.AutoModelExhaustionAccounts(ctx, groupID) {
		seen := make(map[string]bool)
		for _, model := range account.ExhaustionPolicy().Models {
			for _, route := range routes {
				sameModel := route.model == model || route.upstreamModel == model
				if aliases != nil {
					sameModel = sameModel || aliases.Canonicalize(route.model) == aliases.Canonicalize(model)
				}
				if !sameModel || route.targetPlatform != account.Platform || seen[route.upstreamModel] {
					continue
				}
				if !account.IsModelSupported(route.upstreamModel) || !service.AutoModelRequestAccountCompatible(ctx, &account, route.upstreamModel, body) {
					continue
				}
				route.accountID = account.ID
				preferred = append(preferred, route)
				seen[route.upstreamModel] = true
			}
		}
	}
	return append(preferred, routes...)
}

// 观测记录显示真实的账号/模型前缀，避免管理员看到与执行顺序不同的候选计划。
func accountExhaustionRoutePlan(routes []autoModelRouteCandidate, plan []service.AutoModelCandidate) []service.AutoModelCandidate {
	preferred := make([]service.AutoModelCandidate, 0)
	for _, route := range routes {
		if route.accountID == 0 {
			break
		}
		preferred = append(preferred, service.AutoModelCandidate{AccountID: route.accountID, Model: route.model, Platform: route.targetPlatform, Eligible: true, Order: len(preferred) + 1, Reason: "account_exhaustion"})
	}
	if len(preferred) == 0 {
		return plan
	}
	offset := len(preferred)
	for _, entry := range plan {
		if entry.Eligible {
			entry.Order += offset
		}
		preferred = append(preferred, entry)
	}
	return preferred
}
