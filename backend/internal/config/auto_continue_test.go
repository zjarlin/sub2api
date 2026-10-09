package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadAutoContinueConfig(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Gateway.AutoContinue.Enabled)
	require.Equal(t, 2, cfg.Gateway.AutoContinue.MaxRounds)
	require.Equal(t, 60, cfg.Gateway.AutoContinue.JudgeTimeoutSeconds)
	t.Setenv("GATEWAY_AUTO_CONTINUE_ENABLED", "false")
	t.Setenv("GATEWAY_AUTO_CONTINUE_MAX_ROUNDS", "3")
	t.Setenv("GATEWAY_AUTO_CONTINUE_MIN_CONFIDENCE", "0.95")
	cfg, err = Load()
	require.NoError(t, err)
	require.False(t, cfg.Gateway.AutoContinue.Enabled)
	require.Equal(t, 3, cfg.Gateway.AutoContinue.MaxRounds)
	require.Equal(t, 0.95, cfg.Gateway.AutoContinue.MinConfidence)
}
