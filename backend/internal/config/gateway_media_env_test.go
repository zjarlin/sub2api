package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 边缘视觉 / 媒体服务的配置此前只在 config.yaml 里存在时才会被 viper 读取，
// 纯环境变量部署（252 / 天津）即使设置了 GATEWAY_MEDIA_* 也会被静默忽略，
// 导致 /media/* 一直返回 404。这组用例锁住 env → Config 的接线。
func TestLoadGatewayMediaConfig(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.Gateway.Media.Enabled)
		require.Empty(t, cfg.Gateway.Media.URL)
		require.Equal(t, 3600, cfg.Gateway.Media.TimeoutSeconds)
		// 未显式配置 URL 时沿用编排内的服务名。
		require.Equal(t, "http://edge-media:18083", cfg.Gateway.Media.BaseURL())
	})
	t.Run("environment", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("GATEWAY_MEDIA_ENABLED", "true")
		t.Setenv("GATEWAY_MEDIA_URL", "http://edge-media:18083/")
		t.Setenv("GATEWAY_MEDIA_TIMEOUT_SECONDS", "7200")
		cfg, err := Load()
		require.NoError(t, err)
		require.True(t, cfg.Gateway.Media.Enabled)
		require.Equal(t, 7200, cfg.Gateway.Media.TimeoutSeconds)
		require.Equal(t, "http://edge-media:18083", cfg.Gateway.Media.BaseURL())
	})
}

func TestLoadGatewayVisionConfig(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.Gateway.Vision.Enabled)
		require.Equal(t, 300, cfg.Gateway.Vision.TimeoutSeconds)
		require.Equal(t, "http://edge-vision:18081", cfg.Gateway.Vision.BaseURL())
	})
	t.Run("environment", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("GATEWAY_VISION_ENABLED", "true")
		t.Setenv("GATEWAY_VISION_URL", "http://edge-vision:18081")
		t.Setenv("GATEWAY_VISION_TIMEOUT_SECONDS", "600")
		cfg, err := Load()
		require.NoError(t, err)
		require.True(t, cfg.Gateway.Vision.Enabled)
		require.Equal(t, 600, cfg.Gateway.Vision.TimeoutSeconds)
	})
}
