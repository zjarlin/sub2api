package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClearUnsupportedModelIfObservedUsesAtomicCompareAndSet(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}}
		query := `(?s)WITH updated AS .*` + regexp.QuoteMeta("extra = a.extra #- ARRAY['unsupported_models', $1]::text[]") +
			`.*a.id = \$2.*a.deleted_at IS NULL.*a.platform = \$3.*a.type = \$4.*a.credentials = \$5::jsonb.*a.proxy_id IS NOT DISTINCT FROM \$6.*` +
			regexp.QuoteMeta("a.extra->'unsupported_models'->$1 = $7::jsonb") + `.*INSERT INTO scheduler_outbox.*SELECT \$8, updated.id, NULL, NULL FROM updated`
		mock.ExpectExec(query).
			WithArgs("restored", int64(42), service.PlatformOpenAI, service.AccountTypeAPIKey, `{"api_key":"fixture"}`, nil, `{"status_code":404}`, service.SchedulerOutboxEventAccountChanged).
			WillReturnResult(sqlmock.NewResult(0, affected))
		repo := newAccountRepositoryWithSQL(nil, db, nil)
		cleared, err := repo.ClearUnsupportedModelIfObserved(context.Background(), account, " Restored ", map[string]any{"status_code": 404})
		require.NoError(t, err)
		require.Equal(t, affected == 1, cleared)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestClearUnsupportedModelIfObservedPropagatesAtomicWriteFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	writeErr := errors.New("outbox unavailable")
	mock.ExpectExec("WITH updated AS").WillReturnError(writeErr)
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	cleared, err := repo.ClearUnsupportedModelIfObserved(context.Background(), &service.Account{ID: 42}, "restored", map[string]any{})
	require.False(t, cleared)
	require.ErrorIs(t, err, writeErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
