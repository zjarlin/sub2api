package service

import (
	"strings"
	"time"
)

const (
	ModelHealthProbeEnabledKey           = "model_health_probe_enabled"
	ModelHealthProbeIntervalKey          = "model_health_probe_interval_hours"
	defaultModelHealthProbeIntervalHours = 168
)

// ModelProbePolicy controls paid, automatic inference tests. Catalog discovery
// and health observations from real requests do not consume this probe budget.
type ModelProbePolicy struct {
	Enabled  bool
	Interval time.Duration
}

func (a *Account) ModelProbePolicy() ModelProbePolicy {
	policy := ModelProbePolicy{Enabled: true, Interval: defaultModelHealthProbeIntervalHours * time.Hour}
	if a == nil {
		policy.Enabled = false
		return policy
	}
	if enabled, ok := a.Extra[ModelHealthProbeEnabledKey].(bool); ok {
		policy.Enabled = enabled
	}
	var hours int
	switch value := a.Extra[ModelHealthProbeIntervalKey].(type) {
	case float64:
		if value >= 24 && value <= 8760 {
			hours = int(value)
		}
	case int:
		hours = value
	}
	if hours >= 24 && hours <= 8760 {
		policy.Interval = time.Duration(hours) * time.Hour
	}
	return policy
}

func isGPTSeriesModel(model string) bool {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(model)), func(r rune) bool {
		return r == '/' || r == ':' || r == '.'
	})
	for _, part := range parts {
		if strings.HasPrefix(part, "gpt") || strings.HasPrefix(part, "chatgpt") {
			return true
		}
	}
	return false
}

func (a *Account) allowsAutomaticModelProbe(model string) bool {
	return a != nil && a.ModelProbePolicy().Enabled &&
		!isGPTSeriesModel(model) && !isGPTSeriesModel(a.GetMappedModel(model))
}
