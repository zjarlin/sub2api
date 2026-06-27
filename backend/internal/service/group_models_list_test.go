package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestNormalizeGroupModelsListConfig_KeepsValidModelRateMultipliersForSelectedModels(t *testing.T) {
	got := normalizeGroupModelsListConfig(domain.GroupModelsListConfig{
		Enabled: true,
		Models:  []string{" gpt-5.5 ", "gpt-5.4", "gpt-5.5", "", "GPT-5.3"},
		ModelRateMultipliers: map[string]float64{
			"gpt-5.5":    0.5,
			"gpt-5.4":    0,
			"gpt-5.3":    0.75,
			"legacy-gpt": 2,
			" ":          3,
		},
	})

	require.Equal(t, []string{"gpt-5.5", "gpt-5.4", "GPT-5.3"}, got.Models)
	require.Equal(t, map[string]float64{"gpt-5.5": 0.5, "GPT-5.3": 0.75}, got.ModelRateMultipliers)
}

func TestGroupModelRateMultiplier_CaseInsensitiveFallback(t *testing.T) {
	g := &Group{ModelsListConfig: domain.GroupModelsListConfig{
		ModelRateMultipliers: map[string]float64{"GPT-5.5": 0.25},
	}}

	got, ok := g.ModelRateMultiplier("gpt-5.5")
	require.True(t, ok)
	require.Equal(t, 0.25, got)
}

func TestResolveModelRateMultiplier_OverridesFallbackMultiplier(t *testing.T) {
	apiKey := &APIKey{Group: &Group{ModelsListConfig: domain.GroupModelsListConfig{
		ModelRateMultipliers: map[string]float64{"gpt-5.5": 0.33},
	}}}

	require.Equal(t, 0.33, resolveModelRateMultiplier(apiKey, "gpt-5.5", 1.7))
	require.Equal(t, 1.7, resolveModelRateMultiplier(apiKey, "gpt-5.4", 1.7))
}
