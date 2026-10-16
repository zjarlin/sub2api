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

// 生产事故回归：ops_error_logs 中存在 model / upstream_model / Auto 路由记录全为空的行，
// 此时列表查询里「错误模型」列的整体表达式为 NULL。该列曾被扫描进 string，
// 导致 `GET /api/v1/admin/ops/request-errors`（面板「请求错误」弹窗）直接 500，
// 前端把失败吞成空列表，只显示「暂无数据」。
// 不变量：单行空值不得打死整个列表；SQL 侧也要兜底为空串。
func TestOpsListErrorLogsToleratesNullErrorModel(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	values := []driver.Value{
		169006, time.Now(), "request", "invalid_request_error", "client", "gateway", "P2", 400,
		"", "", false, nil, nil, "", "cid", "", "bad request",
		nil, "", nil, nil, "", nil, "", nil, "/v1/responses", true, "/v1/responses", "",
		"auto", "", nil, "", 1, "", nil, "[]",
	}
	columns := make([]string, len(values))
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	mock.ExpectQuery("SELECT e.id").WillReturnRows(sqlmock.NewRows(columns).AddRow(values...))

	result, err := (&opsRepository{db: db}).ListErrorLogs(context.Background(), &service.OpsErrorLogFilter{})
	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	require.Empty(t, result.Errors[0].ErrorModel)
	require.NoError(t, mock.ExpectationsWereMet())
}

// SQL 侧兜底：错误模型列必须自带空串回退，Postgres 不允许再返回 NULL。
func TestOpsListErrorLogsErrorModelColumnHasEmptyFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`(?s)SELECT e\.id.*NULLIF\(BTRIM\(e\.model\), ''\),\s*''\s*\)`).
		WillReturnRows(sqlmock.NewRows([]string{"column_0"}))

	_, err = (&opsRepository{db: db}).ListErrorLogs(context.Background(), &service.OpsErrorLogFilter{})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
