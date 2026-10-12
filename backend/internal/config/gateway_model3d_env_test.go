package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 与 vision/media 一致：注册 viper 默认值后 GATEWAY_MODEL3D_* 环境变量才会生效，
// 否则纯环境变量部署会静默忽略这些开关。
func TestLoadGatewayModel3DConfig(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.Gateway.Model3D.Enabled)
		require.Equal(t, 60, cfg.Gateway.Model3D.TimeoutSeconds)
		require.Equal(t, 168*time.Hour, cfg.Gateway.Model3D.TaskHandleTTL())
		require.Empty(t, cfg.Gateway.Model3D.PriorityList())
	})
	t.Run("environment", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("GATEWAY_MODEL3D_ENABLED", "true")
		t.Setenv("GATEWAY_MODEL3D_TIMEOUT_SECONDS", "120")
		t.Setenv("GATEWAY_MODEL3D_TASK_HANDLE_TTL_HOURS", "24")
		t.Setenv("GATEWAY_MODEL3D_PRIORITY", "tripo, meshy")
		t.Setenv("GATEWAY_MODEL3D_DEFAULT_PROVIDER", "tripo")
		cfg, err := Load()
		require.NoError(t, err)
		require.True(t, cfg.Gateway.Model3D.Enabled)
		require.Equal(t, 120, cfg.Gateway.Model3D.TimeoutSeconds)
		require.Equal(t, 24*time.Hour, cfg.Gateway.Model3D.TaskHandleTTL())
		require.Equal(t, []string{"tripo", "meshy"}, cfg.Gateway.Model3D.PriorityList())
		require.Equal(t, "tripo", cfg.Gateway.Model3D.DefaultProvider)
	})
}
