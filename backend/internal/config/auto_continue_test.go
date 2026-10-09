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
	require.Equal(t, 256<<10, cfg.Gateway.AutoContinue.CompactionChunkBytes)
	require.Equal(t, 32, cfg.Gateway.AutoContinue.CompactionMaxChunks)
	require.Equal(t, 120, cfg.Gateway.AutoContinue.CompactionTimeoutSeconds)
	t.Setenv("GATEWAY_AUTO_CONTINUE_ENABLED", "false")
	t.Setenv("GATEWAY_AUTO_CONTINUE_MAX_ROUNDS", "3")
	t.Setenv("GATEWAY_AUTO_CONTINUE_DECISION_MODEL", "laya")
	t.Setenv("GATEWAY_AUTO_CONTINUE_MIN_CONFIDENCE", "0.95")
	t.Setenv("GATEWAY_AUTO_CONTINUE_COMPACTION_CHUNK_BYTES", "65536")
	t.Setenv("GATEWAY_AUTO_CONTINUE_COMPACTION_MAX_CHUNKS", "4")
	t.Setenv("GATEWAY_AUTO_CONTINUE_COMPACTION_TIMEOUT_SECONDS", "30")
	cfg, err = Load()
	require.NoError(t, err)
	require.False(t, cfg.Gateway.AutoContinue.Enabled)
	require.Equal(t, "laya", cfg.Gateway.AutoContinue.DecisionModel)
	require.Equal(t, 3, cfg.Gateway.AutoContinue.MaxRounds)
	require.Equal(t, 0.95, cfg.Gateway.AutoContinue.MinConfidence)
	require.Equal(t, 65536, cfg.Gateway.AutoContinue.CompactionChunkBytes)
	require.Equal(t, 4, cfg.Gateway.AutoContinue.CompactionMaxChunks)
	require.Equal(t, 30, cfg.Gateway.AutoContinue.CompactionTimeoutSeconds)
}
