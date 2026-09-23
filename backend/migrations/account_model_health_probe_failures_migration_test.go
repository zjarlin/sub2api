package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountModelHealthProbeFailuresCanPersistBeforeFirstSuccess(t *testing.T) {
	body, err := FS.ReadFile("234_account_model_health_probe_failures.sql")
	require.NoError(t, err)
	sql := string(body)

	require.Contains(t, sql, "ALTER COLUMN last_success_at DROP NOT NULL")
	require.Contains(t, sql, "VALUES (health_account_id, health_model, NULL, NEW.finished_at, 'scheduled_test')")
	require.Contains(t, sql, "INSERT INTO account_model_health (account_id, model, last_success_at, last_failure_at, source)")
	require.Contains(t, sql, "FROM ops_error_logs")
}
