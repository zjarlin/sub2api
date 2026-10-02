package handler

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const modelTierIDPrefix = "tier-"

type modelTierVirtualModel struct {
	ID     string
	Label  string
	Models []string
}

func modelTierIndex(model string) (int, bool) {
	if !strings.HasPrefix(model, modelTierIDPrefix) {
		return 0, false
	}
	value := strings.TrimPrefix(model, modelTierIDPrefix)
	if value == "" || value[0] == '0' {
		return 0, false
	}
	index, err := strconv.Atoi(value)
	if err != nil || index <= 0 {
		return 0, false
	}
	return index - 1, true
}

func isModelTierVirtualModel(model string) bool {
	_, ok := modelTierIndex(model)
	return ok
}

func modelTierLabel(index int) string {
	labels := []string{"夯", "强", "中", "省", "垃"}
	if index >= 0 && index < len(labels) {
		return labels[index]
	}
	return "梯" + strconv.Itoa(index+1)
}

func (h *GatewayHandler) modelTierVirtualModels(ctx context.Context, availableModels []string) ([]modelTierVirtualModel, error) {
	if h == nil || h.settingService == nil || h.gatewayService == nil {
		return nil, nil
	}
	policy, err := h.settingService.GetModelFallbackPolicy(ctx)
	if err != nil || policy == nil || !policy.Enabled || len(policy.Tiers) == 0 {
		return nil, err
	}
	aliases := service.ModelAliasesFromContext(ctx)
	listed := make(map[string]struct{}, len(availableModels))
	for _, model := range availableModels {
		listed[aliases.Canonicalize(model)] = struct{}{}
	}
	result := make([]modelTierVirtualModel, 0, len(policy.Tiers))
	for index, tier := range policy.Tiers {
		models := make([]string, 0, len(tier.Models))
		seen := make(map[string]struct{}, len(tier.Models))
		for _, model := range tier.Models {
			canonical := aliases.Canonicalize(model)
			if _, duplicate := seen[canonical]; duplicate {
				continue
			}
			seen[canonical] = struct{}{}
			if _, present := listed[canonical]; present {
				models = append(models, canonical)
			}
		}
		if len(models) == 0 {
			continue
		}
		result = append(result, modelTierVirtualModel{
			ID:     modelTierIDPrefix + strconv.Itoa(index+1),
			Label:  modelTierLabel(index),
			Models: models,
		})
	}
	return result, nil
}

func (h *GatewayHandler) filterModelTierRoutes(ctx context.Context, virtualModel string, routes []autoModelRouteCandidate) ([]autoModelRouteCandidate, bool, error) {
	index, tierModel := modelTierIndex(virtualModel)
	if !tierModel {
		return routes, false, nil
	}
	policy, err := h.settingService.GetModelFallbackPolicy(ctx)
	if err != nil {
		return nil, true, err
	}
	if policy == nil || !policy.Enabled || index < 0 || index >= len(policy.Tiers) {
		return nil, true, nil
	}
	aliases := service.ModelAliasesFromContext(ctx)
	order := make(map[string]int)
	next := 0
	// 选择某个能力梯度后只在该梯度及其更低梯度内降级，不偷偷消耗更高档模型。
	for tierIndex := index; tierIndex < len(policy.Tiers); tierIndex++ {
		for _, model := range policy.Tiers[tierIndex].Models {
			canonical := aliases.Canonicalize(model)
			if _, exists := order[canonical]; exists {
				continue
			}
			order[canonical] = next
			next++
		}
	}
	filtered := make([]autoModelRouteCandidate, 0, len(routes))
	for _, route := range routes {
		if _, allowed := order[aliases.Canonicalize(route.model)]; allowed {
			filtered = append(filtered, route)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return order[aliases.Canonicalize(filtered[i].model)] < order[aliases.Canonicalize(filtered[j].model)]
	})
	return filtered, true, nil
}

func modelTierCatalogDisplayName(modelID string) string {
	index, ok := modelTierIndex(modelID)
	if !ok {
		return ""
	}
	return modelTierLabel(index) + " · " + modelID
}
