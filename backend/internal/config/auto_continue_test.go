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
	require.Equal(t, "typesafe/jev", cfg.Gateway.AutoContinue.DecisionModel)
	require.Equal(t, 2, cfg.Gateway.AutoContinue.MaxRounds)
	require.Equal(t, 60, cfg.Gateway.AutoContinue.JudgeTimeoutSeconds)
	require.Equal(t, 8, cfg.Gateway.AutoContinue.SystemOneTimeoutSeconds)
	require.Equal(t, 256<<10, cfg.Gateway.AutoContinue.CompactionChunkBytes)
	require.Equal(t, 32, cfg.Gateway.AutoContinue.CompactionMaxChunks)
	require.Equal(t, 120, cfg.Gateway.AutoContinue.CompactionTimeoutSeconds)
	require.Empty(t, cfg.Gateway.AutoContinue.PlanModel)
	require.Equal(t, 30, cfg.Gateway.AutoContinue.PlanTimeoutSeconds)
	require.Equal(t, 8, cfg.Gateway.AutoContinue.PlanMaxSteps)
	t.Setenv("GATEWAY_AUTO_CONTINUE_ENABLED", "false")
	t.Setenv("GATEWAY_AUTO_CONTINUE_MAX_ROUNDS", "3")
	t.Setenv("GATEWAY_AUTO_CONTINUE_DECISION_MODEL", "laya")
	t.Setenv("GATEWAY_AUTO_CONTINUE_MIN_CONFIDENCE", "0.95")
	t.Setenv("GATEWAY_AUTO_CONTINUE_SYSTEMONE_TIMEOUT_SECONDS", "3")
	t.Setenv("GATEWAY_AUTO_CONTINUE_COMPACTION_CHUNK_BYTES", "65536")
	t.Setenv("GATEWAY_AUTO_CONTINUE_COMPACTION_MAX_CHUNKS", "4")
	t.Setenv("GATEWAY_AUTO_CONTINUE_COMPACTION_TIMEOUT_SECONDS", "30")
	t.Setenv("GATEWAY_AUTO_CONTINUE_PLAN_MODEL", "text-model")
	t.Setenv("GATEWAY_AUTO_CONTINUE_PLAN_TIMEOUT_SECONDS", "12")
	t.Setenv("GATEWAY_AUTO_CONTINUE_PLAN_MAX_STEPS", "5")
	cfg, err = Load()
	require.NoError(t, err)
	require.False(t, cfg.Gateway.AutoContinue.Enabled)
	require.Equal(t, "laya", cfg.Gateway.AutoContinue.DecisionModel)
	require.Equal(t, 3, cfg.Gateway.AutoContinue.MaxRounds)
	require.Equal(t, 0.95, cfg.Gateway.AutoContinue.MinConfidence)
	require.Equal(t, 3, cfg.Gateway.AutoContinue.SystemOneTimeoutSeconds)
	require.Equal(t, 65536, cfg.Gateway.AutoContinue.CompactionChunkBytes)
	require.Equal(t, 4, cfg.Gateway.AutoContinue.CompactionMaxChunks)
	require.Equal(t, 30, cfg.Gateway.AutoContinue.CompactionTimeoutSeconds)
	require.Equal(t, "text-model", cfg.Gateway.AutoContinue.PlanModel)
	require.Equal(t, 12, cfg.Gateway.AutoContinue.PlanTimeoutSeconds)
	require.Equal(t, 5, cfg.Gateway.AutoContinue.PlanMaxSteps)
}
