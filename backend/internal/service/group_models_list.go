package service

import "strings"

func normalizeGroupModelsListConfig(cfg GroupModelsListConfig) GroupModelsListConfig {
	out := GroupModelsListConfig{Enabled: cfg.Enabled}
	modelRateByOriginalID := make(map[string]float64, len(cfg.ModelRateMultipliers))
	for rawModel, rate := range cfg.ModelRateMultipliers {
		model := strings.TrimSpace(rawModel)
		if model == "" || rate <= 0 {
			continue
		}
		modelRateByOriginalID[model] = rate
	}
	modelRateLookup := buildModelRateMultiplierLookup(modelRateByOriginalID)
	if len(cfg.Models) == 0 {
		out.ModelRateMultipliers = normalizeModelRateMultipliers(nil, modelRateByOriginalID, modelRateLookup)
		return out
	}

	seen := make(map[string]struct{}, len(cfg.Models))
	out.Models = make([]string, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out.Models = append(out.Models, model)
	}
	if len(out.Models) == 0 {
		out.Models = nil
	}
	out.ModelRateMultipliers = normalizeModelRateMultipliers(out.Models, modelRateByOriginalID, modelRateLookup)
	return out
}

func normalizeModelRateMultipliers(models []string, rates map[string]float64, lookup map[string]float64) map[string]float64 {
	if len(rates) == 0 {
		return nil
	}
	if len(models) == 0 {
		out := make(map[string]float64, len(rates))
		for model, rate := range rates {
			out[model] = rate
		}
		return out
	}
	out := make(map[string]float64, len(models))
	for _, model := range models {
		if rate, ok := FindModelRateMultiplier(rates, lookup, model); ok {
			out[model] = rate
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (g *Group) CustomModelsListEnabled() bool {
	return g != nil && g.ModelsListConfig.Enabled && len(g.ModelsListConfig.Models) > 0
}

func (g *Group) PrepareRuntimeCaches() {
	if g == nil {
		return
	}
	g.ModelRateMultiplierLookup = buildModelRateMultiplierLookup(g.ModelsListConfig.ModelRateMultipliers)
}

func (g *Group) ModelRateMultiplier(model string) (float64, bool) {
	if g == nil {
		return 0, false
	}
	return FindModelRateMultiplier(g.ModelsListConfig.ModelRateMultipliers, g.ModelRateMultiplierLookup, model)
}

// FindModelRateMultiplier 按模型名查找模型级倍率，优先精确匹配，再走归一化索引匹配。
func FindModelRateMultiplier(rates map[string]float64, lookup map[string]float64, model string) (float64, bool) {
	if len(rates) == 0 {
		return 0, false
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return 0, false
	}
	if rate, ok := rates[model]; ok && rate > 0 {
		return rate, true
	}
	if len(lookup) == 0 {
		lookup = buildModelRateMultiplierLookup(rates)
	}
	for _, key := range modelRateLookupKeys(model) {
		if rate, ok := lookup[key]; ok && rate > 0 {
			return rate, true
		}
	}
	return 0, false
}

func buildModelRateMultiplierLookup(rates map[string]float64) map[string]float64 {
	if len(rates) == 0 {
		return nil
	}
	out := make(map[string]float64, len(rates))
	for rawModel, rate := range rates {
		key := modelRateLookupKey(rawModel)
		if key == "" || rate <= 0 {
			continue
		}
		out[key] = rate
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func modelRateLookupKey(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func modelRateLookupKeys(model string) []string {
	seen := make(map[string]struct{}, 4)
	out := make([]string, 0, 4)
	add := func(candidate string) {
		key := modelRateLookupKey(candidate)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}

	add(model)
	add(normalizeModelNameForPricing(model))
	add(canonicalizeOpenAIModelAliasSpelling(model))
	add(normalizeKnownOpenAICodexModel(model))
	return out
}
