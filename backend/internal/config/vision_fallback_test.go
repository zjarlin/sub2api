package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadVisionFallbackConfig(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		cfg, err := Load()
		require.NoError(t, err)
		require.True(t, cfg.Gateway.VisionFallback.Enabled)
		require.Empty(t, cfg.Gateway.VisionFallback.Model)
		require.Equal(t, 60, cfg.Gateway.VisionFallback.CandidateTimeoutSeconds)
		require.Equal(t, 120, cfg.Gateway.VisionFallback.TimeoutSeconds)
	})
	t.Run("environment", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("GATEWAY_VISION_FALLBACK_ENABLED", "false")
		t.Setenv("GATEWAY_VISION_FALLBACK_MODEL", "configured-vision-model")
		t.Setenv("GATEWAY_VISION_FALLBACK_CANDIDATE_TIMEOUT_SECONDS", "45")
		t.Setenv("GATEWAY_VISION_FALLBACK_TIMEOUT_SECONDS", "100")
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.Gateway.VisionFallback.Enabled)
		require.Equal(t, "configured-vision-model", cfg.Gateway.VisionFallback.Model)
		require.Equal(t, 45, cfg.Gateway.VisionFallback.CandidateTimeoutSeconds)
		require.Equal(t, 100, cfg.Gateway.VisionFallback.TimeoutSeconds)
	})
}
