package migrations

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDropPlatformCheckConstraintsMigration(t *testing.T) {
	content, err := FS.ReadFile("242_drop_platform_check_constraints.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql,
		"ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;")
	require.Contains(t, sql,
		"ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;")
	require.NotContains(t, sql, "ADD CONSTRAINT")
	require.NotContains(t, sql, "DROP CONSTRAINT IF EXISTS channel_monitors_provider_check")
	require.NotContains(t, sql, "DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check")
}

// Platform membership is validated against the application catalog after 242.
// A later migration must not restore a database whitelist that can drift from it.
//
// 与上游的差异：上游在 242 之后不再维护这两个 CHECK 约束，本仓库（fork）另有一套
// 「新增平台即扩展约束」的迁移（247/248/250/251/256/258/259/265/267/269/271/272，
// 对应各平台的 *_platform_migration_test.go）。这些迁移在本仓库合入上游 242 之前
// 就已发布并在现网库执行（checksum 已登记，不可改写），且本仓库的平台清单
// （internal/domain/platforms.go）与 ent schema 校验始终是平台合法性的权威来源。
// 因此这里只对「非平台引入」的后续迁移保留上游不可回填约束的不变式。
func TestLaterMigrationsDoNotRestorePlatformCheckConstraints(t *testing.T) {
	const droppedAt = "242_drop_platform_check_constraints.sql"
	// fork 的平台引入迁移：与平台清单同步扩展约束，属既有设计，不在本测试约束范围内。
	forkPlatformMigrations := map[string]struct{}{
		"247_add_systemone_decision_platforms.sql": {},
		"248_add_vibex_platform.sql":               {},
		"250_add_deepseek_web_platform.sql":        {},
		"251_add_arena_platform.sql":               {},
		"256_add_cursor_platform.sql":              {},
		"258_unify_systemone_platform.sql":         {},
		"259_add_windsurf_platform.sql":            {},
		"265_add_kilo_platform.sql":                {},
		"267_add_madao_platform.sql":               {},
		"269_add_model3d_platform.sql":             {},
		"271_add_teleagent_platform.sql":           {},
		"272_add_v0_platform.sql":                  {},
	}
	entries, err := fs.ReadDir(FS, ".")
	require.NoError(t, err)
	comments := regexp.MustCompile(`(?ms)/\*.*?\*/|--[^\n]*`)
	addConstraint := regexp.MustCompile(`(?i)\bADD\s+CONSTRAINT\s+"?(user_platform_quotas_platform_check|composite_model_routes_target_platform_check)\b`)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() <= droppedAt {
			continue
		}
		if _, ok := forkPlatformMigrations[entry.Name()]; ok {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			content, err := FS.ReadFile(entry.Name())
			require.NoError(t, err)
			sql := comments.ReplaceAll(content, nil)
			require.Nil(t, addConstraint.Find(sql), "platform CHECK constraints removed in 242 must remain managed by application validation")
		})
	}
}
