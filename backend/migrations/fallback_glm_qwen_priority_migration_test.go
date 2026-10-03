package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFallbackGLMQwenPriorityMigration(t *testing.T) {
	content, err := FS.ReadFile("262_prioritize_glm_qwen_fallback.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'deepseek-v4.1-flash'")
	require.Contains(t, sql, "'gpt-5.6-terra'")
	require.Contains(t, sql, "lower(value) LIKE 'glm-%'")
	require.Contains(t, sql, "lower(value) LIKE 'qwen%'")
	require.Contains(t, sql, "ORDER BY priority, ordinality")
	require.Contains(t, sql, "updated_policy IS DISTINCT FROM stored_policy")
}
