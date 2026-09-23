package repository

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsListErrorLogsPreservesAccountAttemptOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	values := []driver.Value{
		169006, time.Now(), "routing", "rate_limit_error", "platform", "gateway", "P2", 429,
		"openai", "deepseek-v4.1-flash", false, nil, nil, "", "", "request-id", "queue full",
		nil, "", nil, 832, "r4", nil, "", nil, "/v1/responses", true, "/v1/responses", "", "deepseek-v4.1-flash", "", "", 2, "", nil,
		`[{"account_id":837,"account_name":"aaawinn"},{"account_id":832,"account_name":"r4"},{"account_id":832,"account_name":"r4"}]`,
	}
	columns := make([]string, len(values))
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	mock.ExpectQuery("SELECT e.id").WillReturnRows(sqlmock.NewRows(columns).AddRow(values...))
	result, err := (&opsRepository{db: db}).ListErrorLogs(context.Background(), &service.OpsErrorLogFilter{})
	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	require.Equal(t, 429, result.Errors[0].StatusCode)
	require.Equal(t, []service.OpsAccountAttempt{
		{AccountID: 837, AccountName: "aaawinn"},
		{AccountID: 832, AccountName: "r4"},
		{AccountID: 832, AccountName: "r4"},
	}, result.Errors[0].AccountAttempts)
	require.NoError(t, mock.ExpectationsWereMet())
}
