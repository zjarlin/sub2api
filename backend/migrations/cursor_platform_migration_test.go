package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCursorPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("256_add_cursor_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
	for _, field := range []string{"platform", "target_platform"} {
		pattern := regexp.MustCompile(`CHECK \(` + field + ` IN \(([^)]+)\)\)`)
		match := pattern.FindStringSubmatch(sql)
		require.Len(t, match, 2)
		platforms := make([]string, 0)
		for _, entry := range strings.Split(match[1], ",") {
			platforms = append(platforms, strings.Trim(strings.TrimSpace(entry), "'"))
		}
		require.ElementsMatch(t, service.AllowedQuotaPlatforms, platforms)
	}
	require.NotContains(t, sql, "ALTER TABLE channel_monitors")
	require.NotContains(t, sql, "ALTER TABLE channel_monitor_request_templates")
}
