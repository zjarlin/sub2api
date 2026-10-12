package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModel3DPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("269_add_model3d_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
	expected := []string{
		"anthropic", "openai", "gemini", "antigravity", "grok", "kimi", "zhipu",
		"deepseek", "deepseek_web", "arena", "minimax", "cursor", "windsurf",
		"opencode_go", "kilo", "doubao", "traework", "workbuddy", "zcode", "laya", "jev",
		"vibex", "systemone", "madao", "qoder", "model3d",
	}
	for _, field := range []string{"platform", "target_platform"} {
		pattern := regexp.MustCompile(`CHECK \(` + field + ` IN \(([^)]+)\)\)`)
		match := pattern.FindStringSubmatch(sql)
		require.Len(t, match, 2)
		platforms := make([]string, 0)
		for _, entry := range strings.Split(match[1], ",") {
			platforms = append(platforms, strings.Trim(strings.TrimSpace(entry), "'"))
		}
		require.ElementsMatch(t, expected, platforms)
	}
}
