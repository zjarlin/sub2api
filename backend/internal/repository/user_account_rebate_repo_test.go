package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// newRebateRepoTestClient 复用 sqlmock + captureEntQueryMatcher 记录 SQL 的既有测试骨架。
func newRebateRepoTestClient(t *testing.T) (*dbent.Client, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return client, mock
}

// 结算基数必须取 LEAST(倍率前成本, 用户实付)：号主可通过账号计费倍率放大
// 倍率相关成本，只有把用户实付作为上限才能堵住套利。
func TestSettleAccountUsesCappedBasis(t *testing.T) {
	var capturedSQL []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcherList{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &userAccountRebateRepository{client: client}

	windowFrom := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	windowTo := time.Now().UTC().Truncate(time.Second)

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO user_account_rebate_cursors`).
		WithArgs(int64(9), int64(5), windowTo).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT owner_user_id, settled_through`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"owner_user_id", "settled_through", "total_consumed", "total_rebated"}).
			AddRow(int64(5), windowFrom, 0.0, 0.0))
	mock.ExpectQuery(`SUM\(LEAST`).
		WithArgs(int64(9), windowFrom, windowTo, int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"count", "basis"}).AddRow(int64(3), 1.25))
	mock.ExpectQuery(`UPDATE users`).
		WithArgs(sqlmock.AnyArg(), int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(11.25))
	mock.ExpectExec(`INSERT INTO user_account_rebate_ledger`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE user_account_rebate_cursors`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	settlement, err := repo.SettleAccount(context.Background(), service.AccountRebateSettleInput{
		AccountID:         9,
		OwnerUserID:       5,
		WindowTo:          windowTo,
		RatePercent:       100,
		IncludeOwnerUsage: false,
	})
	require.NoError(t, err)
	require.True(t, settlement.Settled)
	require.InDelta(t, 1.25, settlement.ConsumedBasis, 1e-9)
	require.InDelta(t, 1.25, settlement.RebateAmount, 1e-9)
	require.NotNil(t, settlement.BalanceAfter)
	require.InDelta(t, 11.25, *settlement.BalanceAfter, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())

	aggregate := findSQL(capturedSQL, "SUM(LEAST")
	require.Contains(t, aggregate, "LEAST(COALESCE(account_stats_cost, total_cost), actual_cost)")
	require.Contains(t, aggregate, "user_id <> $4")
}

// 号主自用计入时聚合语句不得再带 user_id 排除条件。
func TestSettleAccountIncludesOwnerUsage(t *testing.T) {
	var capturedSQL []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcherList{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &userAccountRebateRepository{client: client}

	windowFrom := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	windowTo := time.Now().UTC().Truncate(time.Second)

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO user_account_rebate_cursors`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT owner_user_id, settled_through`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_user_id", "settled_through", "total_consumed", "total_rebated"}).
			AddRow(int64(5), windowFrom, 0.0, 0.0))
	mock.ExpectQuery(`SUM\(LEAST`).
		WithArgs(int64(9), windowFrom, windowTo).
		WillReturnRows(sqlmock.NewRows([]string{"count", "basis"}).AddRow(int64(0), 0.0))
	mock.ExpectExec(`UPDATE user_account_rebate_cursors`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	settlement, err := repo.SettleAccount(context.Background(), service.AccountRebateSettleInput{
		AccountID:         9,
		OwnerUserID:       5,
		WindowTo:          windowTo,
		RatePercent:       100,
		IncludeOwnerUsage: true,
	})
	require.NoError(t, err)
	require.True(t, settlement.Settled)
	require.Zero(t, settlement.RebateAmount)
	require.NoError(t, mock.ExpectationsWereMet())

	aggregate := findSQL(capturedSQL, "SUM(LEAST")
	require.NotContains(t, aggregate, "user_id <>")
}

// 首次结算只建立基线：游标以「当前窗口右边界」初始化，因此该账号的历史用量
// 不会被追溯返额（返额只从功能开启后的新增消耗开始）。这是防"上线即补发历史"的安全属性。
func TestSettleAccountFirstRunEstablishesBaselineWithoutRetroCredit(t *testing.T) {
	var capturedSQL []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcherList{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &userAccountRebateRepository{client: client}

	windowTo := time.Now().UTC().Truncate(time.Second)

	mock.ExpectBegin()
	// 游标首次创建：settled_through 直接取本次窗口右边界（不回溯历史）。
	mock.ExpectExec(`INSERT INTO user_account_rebate_cursors`).
		WithArgs(int64(9), int64(5), windowTo).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT owner_user_id, settled_through`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_user_id", "settled_through", "total_consumed", "total_rebated"}).
			AddRow(int64(5), windowTo, 0.0, 0.0))
	mock.ExpectCommit()

	settlement, err := repo.SettleAccount(context.Background(), service.AccountRebateSettleInput{
		AccountID:   9,
		OwnerUserID: 5,
		WindowTo:    windowTo,
		RatePercent: 100,
	})
	require.NoError(t, err)
	require.True(t, settlement.Skipped)
	require.Zero(t, settlement.RebateAmount)
	require.NoError(t, mock.ExpectationsWereMet())

	// 首次运行不得出现聚合、入账或流水写入。
	require.Empty(t, findSQL(capturedSQL, "SUM(LEAST"))
	require.Empty(t, findSQL(capturedSQL, "UPDATE users"))
	require.Empty(t, findSQL(capturedSQL, "INSERT INTO user_account_rebate_ledger"))
}

// 空窗口（右边界不晚于已结算位置）直接跳过，不聚合、不入账，但仍锁定游标。
func TestSettleAccountSkipsEmptyWindow(t *testing.T) {
	db, mock := newRebateRepoTestClient(t)
	repo := &userAccountRebateRepository{client: db}

	settled := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO user_account_rebate_cursors`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT owner_user_id, settled_through`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_user_id", "settled_through", "total_consumed", "total_rebated"}).
			AddRow(int64(5), settled, 0.0, 0.0))
	mock.ExpectCommit()

	settlement, err := repo.SettleAccount(context.Background(), service.AccountRebateSettleInput{
		AccountID:   9,
		OwnerUserID: 5,
		WindowTo:    settled.Add(-time.Minute),
		RatePercent: 100,
	})
	require.NoError(t, err)
	require.True(t, settlement.Skipped)
	require.False(t, settlement.Settled)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 候选账号查询必须只取号主非空且未删除的账号，并支持"仅共享"过滤。
func TestListCandidateAccountsSQL(t *testing.T) {
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &userAccountRebateRepository{client: client}

	mock.ExpectQuery("candidates").
		WithArgs(true, 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "owner_user_id"}).AddRow(int64(9), int64(5)))

	candidates, err := repo.ListCandidateAccounts(context.Background(), true, 50)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, int64(9), candidates[0].AccountID)
	require.Equal(t, int64(5), candidates[0].OwnerUserID)
	require.NoError(t, mock.ExpectationsWereMet())

	normalized := normalizeSQLWhitespace(capturedSQL)
	require.Contains(t, normalized, "a.deleted_at IS NULL")
	require.Contains(t, normalized, "a.owner_user_id IS NOT NULL")
	require.Contains(t, normalized, "shared_for_public_scheduling")
}

type captureEntQueryMatcherList struct {
	actual *[]string
}

func (m captureEntQueryMatcherList) Match(_, actual string) error {
	*m.actual = append(*m.actual, actual)
	return nil
}

func findSQL(list []string, contains string) string {
	for _, q := range list {
		if strings.Contains(q, contains) {
			return normalizeSQLWhitespace(q)
		}
	}
	return ""
}
