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
