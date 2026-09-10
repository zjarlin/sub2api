package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListModelHealthObservationsFiltersByGroupAndPlatform(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	checkedAt := time.Date(2026, 9, 10, 15, 15, 33, 0, time.UTC)
	mock.ExpectQuery("SELECT h.account_id, h.model, h.last_success_at").
		WithArgs(service.PlatformOpenAI, int64(6)).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "model", "checked_at"}).
			AddRow(int64(820), "gpt-6-astra", checkedAt))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	groupID := int64(6)
	observations, err := repo.ListModelHealthObservations(
		context.Background(),
		&groupID,
		service.PlatformOpenAI,
	)
	require.NoError(t, err)
	require.Equal(t, []service.ModelHealthObservation{{
		AccountID: 820,
		Model:     "gpt-6-astra",
		CheckedAt: checkedAt,
	}}, observations)
	require.NoError(t, mock.ExpectationsWereMet())
}
