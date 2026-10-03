package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoModelRoutesRequestIDIndexMigration(t *testing.T) {
	content, err := FS.ReadFile("264_auto_model_routes_request_id_index_notx.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_auto_model_routes_request_id")
	require.Contains(t, sql, "ON auto_model_routes ((request_id::text), api_key_id)")
}
