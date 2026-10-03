package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeepSeekV41FlashHyphenAliasMigration(t *testing.T) {
	content, err := FS.ReadFile("260_deepseek_v41_flash_hyphen_alias.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'deepseek-v4.1-flash'")
	require.Contains(t, sql, "'deepseek-v4-1-flash'")
	require.Contains(t, sql, "stored_policy->'groups' = '[]'::jsonb")
	require.Contains(t, sql, "updated_policy IS DISTINCT FROM stored_policy")
}
