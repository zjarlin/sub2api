package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newUnsupportedModelRepositoryTest(t *testing.T) (*accountRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return newAccountRepositoryWithSQL(client, nil, nil), mock
}

func TestAccountRepositorySetUnsupportedModelUsesAtomicJSONBUpdate(t *testing.T) {
	repo, mock := newUnsupportedModelRepositoryTest(t)
	observation := service.UnsupportedModelObservation{
		DetectedAt: time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC),
		StatusCode: 404,
		Reason:     "upstream_404_model_not_found",
		Message:    "model not found",
	}
	mock.ExpectExec(`(?s)`+regexp.QuoteMeta("UPDATE accounts SET")+`.*jsonb_set.*unsupported_models.*ARRAY\['unsupported_models', \$1\].*WHERE id = \$3`).
		WithArgs("gpt-6-astra", sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.SetUnsupportedModel(context.Background(), 42, " GPT-6-ASTRA ", observation)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountRepositoryClearUnsupportedModels(t *testing.T) {
	repo, mock := newUnsupportedModelRepositoryTest(t)
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = .* - 'unsupported_models'.*WHERE id = \$1`).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.ClearUnsupportedModels(context.Background(), 42)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
