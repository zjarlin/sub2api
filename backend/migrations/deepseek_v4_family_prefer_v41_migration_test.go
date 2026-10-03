package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeepSeekV4FamilyPreferV41Migration(t *testing.T) {
	content, err := FS.ReadFile("261_deepseek_v4_family_prefer_v41.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'deepseek-v4.1-flash'")
	require.Contains(t, sql, "ARRAY['deepseek-v4-flash', 'deepseek-v4-pro']")
	require.Contains(t, sql, "reserved_ids ? model_id")
	require.Contains(t, sql, "jsonb_set(stored_policy, '{groups}', updated_groups)")
	require.Contains(t, sql, "updated_policy IS DISTINCT FROM stored_policy")
}
