package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRecoveredSuccessListUsesFinalOutcome(t *testing.T) {
	where, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{
		View: "recovered", IncludeRecoveredUpstream: true,
		ErrorPhasesAny: []string{"upstream", "account_auth", "routing"},
	})
	require.Contains(t, where, opsRecoveredSuccessPredicate)
	require.NotContains(t, where, "COALESCE(e.status_code, 0) >= 400")
	require.NotContains(t, where, "COALESCE(e.is_business_limited,false)")
	require.Contains(t, where, "NULLIF(e.request_id, '') IS NOT NULL")
	require.Contains(t, where, "final.request_id = e.request_id")
	require.Contains(t, where, "COALESCE(final.status_code, 0) >= 400")
	require.Contains(t, where, "COALESCE(final.error_message, '') NOT LIKE 'Recovered %'", "历史带内失败即使保留 200 也不能归入成功")

	requestErrors, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{View: "recovered"})
	require.Contains(t, requestErrors, "COALESCE(e.status_code, 0) >= 400", "请求错误端点不能绕过最终失败守卫")
	for _, view := range []string{"", "errors", "excluded"} {
		failures, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{
			View: view, IncludeRecoveredUpstream: true,
			ErrorPhasesAny: []string{"upstream", "account_auth", "routing"},
		})
		require.Contains(t, failures, "COALESCE(e.status_code, 0) >= 400", "错误视图不能包含已恢复请求")
	}
}

func TestRecoveredSuccessCountMatchesListScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	groupID := int64(6)
	filter := &service.OpsDashboardFilter{StartTime: start, EndTime: end, Platform: "openai", GroupID: &groupID}
	where, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{
		StartTime: &start, EndTime: &end, Platform: "openai", GroupID: &groupID,
		View: "recovered", IncludeRecoveredUpstream: true,
		ErrorPhasesAny: []string{"upstream", "account_auth", "routing"},
	})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM ops_error_logs e "+where)).
		WithArgs(start, end, "openai", groupID, pq.Array([]string{"upstream", "account_auth", "routing"})).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	count, err := (&opsRepository{db: db}).queryRecoveredSuccessCount(context.Background(), filter)
	require.NoError(t, err)
	require.EqualValues(t, 3, count)
	require.NoError(t, mock.ExpectationsWereMet())
}
