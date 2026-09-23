package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountModelHealthUsageTreatsZeroCostAsSuccess(t *testing.T) {
	body, err := FS.ReadFile("233_account_model_health_zero_cost_success.sql")
	require.NoError(t, err)
	sql := string(body)

	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION record_usage_account_model_health")
	require.Contains(t, sql, "FROM usage_logs")
	require.Contains(t, sql, "INSERT INTO account_model_health")
	require.NotContains(t, strings.ToLower(sql), "actual_cost")
}
