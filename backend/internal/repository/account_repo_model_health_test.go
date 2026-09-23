package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestRecordAccountModelHealthSuccessUpsertsProbeResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	checkedAt := time.Date(2026, 9, 10, 18, 30, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO account_model_health (account_id, model, last_success_at, source)")).
		WithArgs(int64(287), "agnes-2.0-flash", checkedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	err = repo.RecordAccountModelHealthSuccess(context.Background(), 287, " agnes-2.0-flash ", checkedAt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordAccountModelHealthFailureUpsertsProbeResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	checkedAt := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO account_model_health (account_id, model, last_success_at, last_failure_at, source)")).
		WithArgs(int64(287), "agnes-2.5-flash", checkedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	err = repo.RecordAccountModelHealthFailure(context.Background(), 287, " agnes-2.5-flash ", checkedAt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccountModelHealthStatesPreservesFailureOnlyRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	failureAt := time.Date(2026, 9, 10, 19, 10, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT account_id, model, last_success_at, last_failure_at").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "model", "last_success_at", "last_failure_at"}).
			AddRow(int64(287), "agnes-2.5-flash", nil, failureAt))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	states, err := repo.ListAccountModelHealthStates(context.Background())
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Nil(t, states[0].LastSuccessAt)
	require.Equal(t, failureAt, *states[0].LastFailureAt)
	require.NoError(t, mock.ExpectationsWereMet())
}
