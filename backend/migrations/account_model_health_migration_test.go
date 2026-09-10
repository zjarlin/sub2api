package migrations

import (
	"strings"
	"testing"
)

func TestAccountModelHealthMigrationBackfillsAndMaintainsDurableEvidence(t *testing.T) {
	body, err := FS.ReadFile("231_account_model_health.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(body)
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS account_model_health",
		"INSERT INTO account_model_health",
		"FROM usage_logs ul",
		"JOIN scheduled_test_results",
		"CREATE TRIGGER trg_usage_account_model_health",
		"CREATE TRIGGER trg_scheduled_test_account_model_health",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("migration missing %q", expected)
		}
	}
}
