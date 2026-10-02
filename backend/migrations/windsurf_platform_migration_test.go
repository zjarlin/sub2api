package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindsurfPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("259_add_windsurf_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
	// 迁移不可变：固定 259 写下的平台集合。
	expected := []string{
		"anthropic", "openai", "gemini", "antigravity", "grok", "kimi", "zhipu",
		"deepseek", "deepseek_web", "arena", "minimax", "cursor", "windsurf",
		"opencode_go", "doubao", "traework", "workbuddy", "zcode", "laya", "jev",
		"vibex", "systemone",
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
	require.NotContains(t, sql, "ALTER TABLE channel_monitors")
}
