package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMadaoPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("267_add_madao_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
	// 迁移不可变：固定 267 写下的平台集合。
	expected := []string{
		"anthropic", "openai", "gemini", "antigravity", "grok", "kimi", "zhipu",
		"deepseek", "deepseek_web", "madao", "arena", "minimax", "cursor", "windsurf",
		"opencode_go", "kilo", "doubao", "traework", "workbuddy", "vibex", "zcode",
		"laya", "jev", "systemone",
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
	// 码道不是频道监控供应商，迁移不得改动 channel_monitors。
	require.NotContains(t, sql, "ALTER TABLE channel_monitors")
}
