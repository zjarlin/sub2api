package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelFallbackGuardTierAndAutoMigration(t *testing.T) {
	content, err := FS.ReadFile("270_model_fallback_guard_tier_and_auto.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "key = 'model_fallback_policy'")
	require.Contains(t, sql, "{auto_fallback}")
	// 幂等：仅当同名档位不存在时才追加。
	require.Contains(t, sql, "WHERE tier->>'name' = '内容安全轻量'")
	for _, model := range []string{
		"meta/llama-guard-4-12b",
		"nvidia/llama-3.1-nemoguard-8b-content-safety",
		"nvidia/llama-3.1-nemoguard-8b-topic-control",
		"nvidia/llama-3.1-nemotron-safety-guard-8b-v3",
	} {
		require.Contains(t, sql, model)
	}
	// 未配置策略时不得伪造档位。
	require.Contains(t, sql, "IF stored_policy IS NULL OR stored_policy->'tiers' IS NULL THEN RETURN; END IF;")
}
