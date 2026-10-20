package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryListRequestDetails_LatencySort(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sort  string
		order string
	}{
		{name: "TTFT", sort: "ttft_desc", order: "first_token_ms DESC NULLS LAST, created_at DESC"},
		{name: "duration", sort: "duration_desc", order: "duration_ms DESC NULLS LAST, created_at DESC"},
		{name: "default", order: "created_at DESC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := &opsRepository{db: db}
			start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			end := start.Add(time.Hour)
			filter := &service.OpsRequestDetailFilter{
				StartTime: &start,
				EndTime:   &end,
				Sort:      tc.sort,
				Page:      2,
				PageSize:  10,
			}

			mock.ExpectQuery(`SELECT COUNT\(1\) FROM combined`).
				WithArgs(start, end).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(13))
			rows := sqlmock.NewRows([]string{
				"kind", "created_at", "request_id", "platform", "model", "requested_model", "upstream_model", "error_model", "duration_ms", "first_token_ms",
				"status_code", "error_id", "phase", "severity", "message", "user_id", "user_email", "username", "user_account", "api_key_id", "account_id", "account_name", "group_id", "stream",
			}).
				AddRow("success", start, "req-slow", "openai", "gpt-5.5", "auto", "gpt-5.5", nil, 12000, 800, nil, nil, nil, nil, nil, 1, "user@example.com", "user", "user", 2, 3, "dispatch-3", 4, true).
				AddRow("error", start, "req-zero", "openai", "glm-5.2", "auto", "glm-5.2", "glm-5.2", 9000, 0, 502, 5, "upstream", "error", "failed", 1, "user@example.com", "user", "user", 2, 3, "dispatch-3", 4, true).
				AddRow("success", start, "req-missing", "openai", "gpt-5.5", "gpt-5.5", "gpt-5.5", nil, 5000, nil, nil, nil, nil, nil, nil, 1, "user@example.com", "user", "user", 2, 3, "dispatch-3", 4, false)
			mock.ExpectQuery(`(?s)ul\.first_token_ms AS first_token_ms.*o\.time_to_first_token_ms AS first_token_ms.*SELECT.*duration_ms,\s+first_token_ms,.*ORDER BY `+tc.order+`\s+LIMIT \$3 OFFSET \$4`).
				WithArgs(start, end, 10, 10).
				WillReturnRows(rows)

			items, total, err := repo.ListRequestDetails(context.Background(), filter)
			require.NoError(t, err)
			require.EqualValues(t, 13, total)
			require.Len(t, items, 3)
			require.NotNil(t, items[0].FirstTokenMs)
			require.Equal(t, 800, *items[0].FirstTokenMs)
			require.Equal(t, 12000, *items[0].DurationMs)
			require.NotNil(t, items[1].FirstTokenMs)
			require.Zero(t, *items[1].FirstTokenMs)
			require.Nil(t, items[2].FirstTokenMs)
			require.Equal(t, "auto", items[0].RequestedModel)
			require.Equal(t, "gpt-5.5", items[0].UpstreamModel)
			require.Equal(t, "glm-5.2", items[1].ErrorModel)
			require.Equal(t, "dispatch-3", items[1].AccountName)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// 调度账号搜索必须同时匹配账号名与账号 ID，并容忍「#905」这种带井号粘贴。
func TestOpsRepositoryListRequestDetails_FiltersByDispatchAccount(t *testing.T) {
	var capturedSQL []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcherList{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &opsRepository{db: db}

	start := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	filter := &service.OpsRequestDetailFilter{
		StartTime:   &start,
		EndTime:     &end,
		AccountName: "#yueyue_workbuddy",
		Page:        1,
		PageSize:    10,
	}

	mock.ExpectQuery(`SELECT COUNT\(1\) FROM combined`).
		WithArgs(start, end, "%yueyue_workbuddy%", "yueyue_workbuddy").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`FROM combined`).
		WithArgs(start, end, "%yueyue_workbuddy%", "yueyue_workbuddy", 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"kind", "created_at", "request_id", "platform", "model", "requested_model", "upstream_model", "error_model", "duration_ms", "first_token_ms",
			"status_code", "error_id", "phase", "severity", "message", "user_id", "user_email", "username", "user_account", "api_key_id", "account_id", "account_name", "group_id", "stream",
		}).AddRow("success", start, "req-1", "openai", "deepseek-v4.1-flash", "deepseek-v4.1-flash", "cn:deepseek-v4.1-flash", nil, 6305, 100, nil, nil, nil, nil, nil, 4, "zjarlin@outlook.com", "zjarlin", "zjarlin", 1, 905, "yueyue_workbuddy", 2, true))

	items, total, err := repo.ListRequestDetails(context.Background(), filter)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, "yueyue_workbuddy", items[0].AccountName)
	require.NotNil(t, items[0].AccountID)
	require.Equal(t, int64(905), *items[0].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())

	condition := findSQL(capturedSQL, "account_name")
	require.Contains(t, condition, "LOWER(COALESCE(account_name,'')) LIKE $3")
	require.Contains(t, condition, "account_id::text = $4")
}

// 模型搜索必须同时命中请求模型与实际调度模型，忽略大小写，并转义 LIKE 通配符。
func TestOpsRepositoryListRequestDetails_FiltersByModelQuery(t *testing.T) {
	var capturedSQL []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcherList{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &opsRepository{db: db}

	start := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	filter := &service.OpsRequestDetailFilter{
		StartTime:  &start,
		EndTime:    &end,
		ModelQuery: "DeepSeek-V4.1",
		Page:       1,
		PageSize:   10,
	}

	mock.ExpectQuery(`SELECT COUNT\(1\) FROM combined`).
		WithArgs(start, end, "%deepseek-v4.1%", "%deepseek-v4.1%", "%deepseek-v4.1%", "%deepseek-v4.1%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`FROM combined`).
		WithArgs(start, end, "%deepseek-v4.1%", "%deepseek-v4.1%", "%deepseek-v4.1%", "%deepseek-v4.1%", 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"kind", "created_at", "request_id", "platform", "model", "requested_model", "upstream_model", "error_model", "duration_ms", "first_token_ms",
			"status_code", "error_id", "phase", "severity", "message", "user_id", "user_email", "username", "user_account", "api_key_id", "account_id", "account_name", "group_id", "stream",
		}).
			AddRow("success", start, "req-auto", "openai", "deepseek-v4.1-flash", "auto", "deepseek-v4.1-flash", nil, 6305, 100, nil, nil, nil, nil, nil, 4, "zjarlin@outlook.com", "zjarlin", "zjarlin", 1, 832, "r4-codes", 2, true).
			AddRow("error", start, "req-direct", "openai", "deepseek-v4.1-flash", "deepseek-v4.1-flash", "deepseek-v4.1-flash", "deepseek-v4.1-flash", 9000, nil, 503, 5, "upstream", "error", "failed", 4, "zjarlin@outlook.com", "zjarlin", "zjarlin", 1, 832, "r4-codes", 2, true))

	items, total, err := repo.ListRequestDetails(context.Background(), filter)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	require.NoError(t, mock.ExpectationsWereMet())

	condition := findSQL(capturedSQL, "WHERE (LOWER(COALESCE(requested_model,'')) LIKE")
	require.NotEmpty(t, condition)
	for _, column := range []string{"requested_model", "model", "upstream_model", "error_model"} {
		require.Contains(t, condition, "LOWER(COALESCE("+column+",'')) LIKE")
	}
	require.Contains(t, condition, "LOWER(COALESCE(model,'')) LIKE")
}

func TestOpsRepositoryListRequestDetails_ModelQueryEscapesWildcards(t *testing.T) {
	var capturedSQL []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcherList{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &opsRepository{db: db}

	start := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	filter := &service.OpsRequestDetailFilter{
		StartTime:  &start,
		EndTime:    &end,
		ModelQuery: "gpt_100%",
		Page:       1,
		PageSize:   10,
	}

	mock.ExpectQuery(`SELECT COUNT\(1\) FROM combined`).
		WithArgs(start, end, `%gpt\_100\%%`, `%gpt\_100\%%`, `%gpt\_100\%%`, `%gpt\_100\%%`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`FROM combined`).
		WithArgs(start, end, `%gpt\_100\%%`, `%gpt\_100\%%`, `%gpt\_100\%%`, `%gpt\_100\%%`, 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"kind", "created_at", "request_id", "platform", "model", "requested_model", "upstream_model", "error_model", "duration_ms", "first_token_ms",
			"status_code", "error_id", "phase", "severity", "message", "user_id", "user_email", "username", "user_account", "api_key_id", "account_id", "account_name", "group_id", "stream",
		}))

	_, total, err := repo.ListRequestDetails(context.Background(), filter)
	require.NoError(t, err)
	require.EqualValues(t, 0, total)
	require.NoError(t, mock.ExpectationsWereMet())
}
