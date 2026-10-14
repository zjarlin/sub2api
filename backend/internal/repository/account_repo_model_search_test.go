package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// 「账号管理」的搜索框要能用模型 ID 反查调度账号：搜 Doubao-Seed-2.0-Code 时，
// 除了账号名/ID，还要命中 credentials.model_mapping 与上游目录快照里的该模型。
func TestListWithFilters_SearchMatchesModelID(t *testing.T) {
	var queries []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcherList{actual: &queries}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	mock.ExpectQuery("count filtered accounts").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("list filtered accounts").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	accounts, page, err := repo.ListWithFilters(
		context.Background(),
		pagination.PaginationParams{Page: 1, PageSize: 20},
		"", "", "", "Doubao-Seed-2.0-Code", 0, "", nil, nil,
	)
	require.NoError(t, err)
	require.Empty(t, accounts)
	require.NotNil(t, page)
	require.Equal(t, int64(0), page.Total)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, queries, 2, "ListWithFilters must run exactly the count and list queries")

	listSQL := normalizeSQLWhitespace(queries[len(queries)-1])
	require.Contains(t, listSQL, `"name" ILIKE`)
	require.Contains(t, listSQL, `->>'model_mapping' ILIKE`)
	require.Contains(t, listSQL, `->>'compact_model_mapping' ILIKE`)
	require.Contains(t, listSQL, `'upstream_supported_models'->>'models' ILIKE`)
}
