package service

import (
	"strings"
	"time"
)

const (
	ModelHealthProbeEnabledKey      = "model_health_probe_enabled"
	ModelHealthProbeIntervalKey     = "model_health_probe_interval_hours"
	MinimumModelHealthProbeInterval = 7 * 24 * time.Hour
)

// 自动付费探测至少间隔一周；目录发现和真实请求健康记录不额外消耗探测额度。
type ModelProbePolicy struct {
	Enabled  bool
	Interval time.Duration
}

func (a *Account) ModelProbePolicy() ModelProbePolicy {
	policy := ModelProbePolicy{Enabled: true, Interval: MinimumModelHealthProbeInterval}
	if a == nil || isArenaSessionAdapter(a) {
		policy.Enabled = false
		return policy
	}
	if enabled, ok := a.Extra[ModelHealthProbeEnabledKey].(bool); ok {
		policy.Enabled = enabled
	}
	var hours int
	switch value := a.Extra[ModelHealthProbeIntervalKey].(type) {
	case float64:
		if value >= MinimumModelHealthProbeInterval.Hours() && value <= 8760 {
			hours = int(value)
		}
	case int:
		hours = value
	}
	if hours >= int(MinimumModelHealthProbeInterval.Hours()) && hours <= 8760 {
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
