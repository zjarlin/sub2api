package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAliasDeepSeekV41Flash1017Migration(t *testing.T) {
	content, err := FS.ReadFile("274_alias_deepseek_v41_flash_1017.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'deepseek/deepseek-v4.1-flash-1017'")
	require.Contains(t, sql, "'deepseek-v4.1-flash'")
	require.Contains(t, sql, "stored_policy->'groups' = '[]'::jsonb")
	require.Contains(t, sql, "updated_policy IS DISTINCT FROM stored_policy")
}
