package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
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
	mock.ExpectQuery("SELECT account_id, model, last_success_at, last_failure_at, last_probe_at").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "model", "last_success_at", "last_failure_at", "last_probe_at"}).
			AddRow(int64(287), "agnes-2.5-flash", nil, failureAt, failureAt))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	states, err := repo.ListAccountModelHealthStates(context.Background())
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Nil(t, states[0].LastSuccessAt)
	require.Equal(t, failureAt, *states[0].LastFailureAt)
	require.Equal(t, failureAt, *states[0].LastProbeAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimAccountModelProbeUsesAtomicWeeklyCooldown(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		mock.ExpectExec(`(?s)INSERT INTO account_model_health.*last_probe_at.*CURRENT_TIMESTAMP.*WHERE NOT EXISTS.*ON CONFLICT.*SET last_probe_at = EXCLUDED.last_probe_at.*WHERE COALESCE`).
			WithArgs(int64(1), "model", service.MinimumModelHealthProbeInterval.Microseconds()).
			WillReturnResult(sqlmock.NewResult(0, affected))
		repo := newAccountRepositoryWithSQL(nil, db, nil)
		claimed, err := repo.ClaimAccountModelProbe(context.Background(), 1, " MODEL ", 24*time.Hour)
		require.NoError(t, err)
		require.Equal(t, affected == 1, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestClaimAccountModelProbePropagatesPersistenceFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	writeErr := errors.New("database unavailable")
	mock.ExpectExec("INSERT INTO account_model_health").
		WithArgs(int64(1), "model", (336 * time.Hour).Microseconds()).WillReturnError(writeErr)
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	claimed, err := repo.ClaimAccountModelProbe(context.Background(), 1, "model", 336*time.Hour)
	require.False(t, claimed)
	require.ErrorIs(t, err, writeErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
