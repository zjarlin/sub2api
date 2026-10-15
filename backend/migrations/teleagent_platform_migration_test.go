package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/ent/compositemodelroute"
	"github.com/Wei-Shaw/sub2api/ent/group"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/ent/userplatformquota"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestTeleagentPlatformMigrationAndEntValidation(t *testing.T) {
	previous, err := FS.ReadFile("269_add_model3d_platform.sql")
	require.NoError(t, err)
	current, err := FS.ReadFile("271_add_teleagent_platform.sql")
	require.NoError(t, err)
	for _, field := range []string{"platform", "target_platform"} {
		pattern := regexp.MustCompile(`CHECK \(` + field + ` IN \(([^)]+)\)\)`)
		before := pattern.FindStringSubmatch(string(previous))
		after := pattern.FindStringSubmatch(string(current))
		require.Len(t, before, 2)
		require.Len(t, after, 2)
		expected := strings.Split(before[1], ", ")
		expected = append(expected, "'teleagent'")
		require.ElementsMatch(t, expected, strings.Split(after[1], ", "))
	}
	// 直接调用生成代码实际使用的校验器，防止 schema 已改但运行时仍拒绝平台。
	require.NoError(t, account.PlatformValidator(domain.PlatformTeleagent))
	require.NoError(t, group.PlatformValidator(domain.PlatformTeleagent))
	require.NoError(t, compositemodelroute.TargetPlatformValidator(domain.PlatformTeleagent))
	require.NoError(t, userplatformquota.PlatformValidator(domain.PlatformTeleagent))
	require.Error(t, userplatformquota.PlatformValidator("unknown-platform"))
}
