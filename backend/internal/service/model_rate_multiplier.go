package service

// resolveModelRateMultiplier 在分组配置了模型级倍率时返回模型覆盖值。
// 模型级倍率比最终分组/用户专属倍率更细，计费应优先跟随模型配置。
func resolveModelRateMultiplier(apiKey *APIKey, billingModel string, fallbackMultiplier float64) float64 {
	if apiKey != nil && apiKey.Group != nil {
		if multiplier, ok := apiKey.Group.ModelRateMultiplier(billingModel); ok {
			return multiplier
		}
	}
	return fallbackMultiplier
}
