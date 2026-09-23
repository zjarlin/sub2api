package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestPublicSchedulingPredicateAppliesToAllAndGroupedCandidates(t *testing.T) {
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	mock.ExpectQuery("schedulable loads").WillReturnRows(sqlmock.NewRows([]string{"id", "concurrency", "load_factor"}))
	_, err = repo.ListSchedulableAccountLoads(context.Background())
	require.NoError(t, err)
	require.Contains(t, capturedSQL, "owner_user_id")
	require.Contains(t, capturedSQL, "shared_for_public_scheduling")

	mock.ExpectQuery("group accounts").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = repo.ListSchedulableByGroupID(context.Background(), 7)
	require.NoError(t, err)
	require.Contains(t, capturedSQL, "owner_user_id")
	require.Contains(t, capturedSQL, "shared_for_public_scheduling")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerMetadataRetainsPublicSharingBoundary(t *testing.T) {
	owner := int64(11)
	account := service.Account{
		OwnerUserID: &owner,
		Extra: map[string]any{
			service.AccountPublicSharingExtraKey: false,
			"unrelated":                          "omit",
		},
	}
	metadata := buildSchedulerMetadataAccount(account)
	require.Equal(t, &owner, metadata.OwnerUserID)
	require.False(t, metadata.IsPubliclyShared())
	require.Equal(t, false, metadata.Extra[service.AccountPublicSharingExtraKey])
	require.NotContains(t, metadata.Extra, "unrelated")

	account.Extra[service.AccountPublicSharingExtraKey] = true
	sharedMetadata := buildSchedulerMetadataAccount(account)
	require.True(t, sharedMetadata.IsPubliclyShared())
}
