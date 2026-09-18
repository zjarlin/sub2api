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
	})
	t.Run("environment", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("GATEWAY_VISION_FALLBACK_ENABLED", "false")
		t.Setenv("GATEWAY_VISION_FALLBACK_MODEL", "configured-vision-model")
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.Gateway.VisionFallback.Enabled)
		require.Equal(t, "configured-vision-model", cfg.Gateway.VisionFallback.Model)
	})
}
