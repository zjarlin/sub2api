package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddGLM52SensenovaFallbackMigration(t *testing.T) {
	content, err := FS.ReadFile("263_add_glm52_sensenova_fallback.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'glm-5.2'")
	require.Contains(t, sql, "'sensenova-6.8-flash-lite'")
	require.Contains(t, sql, "WHERE tier->'models' ? 'glm-5.2'")
	require.Contains(t, sql, "(ordinality - 1)::integer <> tier_index")
	require.Contains(t, sql, "jsonb_build_array('sensenova-6.8-flash-lite')")
	require.Contains(t, sql, "updated_policy IS DISTINCT FROM stored_policy")
}
