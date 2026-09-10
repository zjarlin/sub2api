package migrations

import (
	"strings"
	"testing"
)

func TestAccountModelHealthFailuresMigrationTracksLatestOutcome(t *testing.T) {
	body, err := FS.ReadFile("232_account_model_health_failures.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(body)
	for _, expected := range []string{
		"ADD COLUMN IF NOT EXISTS last_failure_at",
		"FROM ops_error_logs",
		"CREATE OR REPLACE FUNCTION record_usage_account_model_health",
		"CREATE OR REPLACE FUNCTION record_scheduled_test_account_model_health",
		"CREATE TRIGGER trg_ops_error_account_model_health",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("migration missing %q", expected)
		}
	}
}
