package domain

// GroupModelsListConfig 控制分组自定义 /v1/models 展示列表与模型级计费倍率。
type GroupModelsListConfig struct {
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models,omitempty"`
	// ModelRateMultipliers 为指定模型覆盖最终生效的分组/用户专属倍率。
	// key 为客户端可见的计费模型 ID，value 必须大于 0。
	ModelRateMultipliers map[string]float64 `json:"model_rate_multipliers,omitempty"`
}
