//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestTurnActionRepositoryScopesEveryReadAndWriteAndClaimsOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &usageLogRepository{sql: db}
	ctx := context.Background()
	value := service.TurnActionRecommendation{SessionID: "019ccb31-9520-7120-bc17-556e9a92d860", RunID: "turn", ContextID: "hash", State: "pending", UpdatedAt: 123, Actions: []service.TurnActionSuggestion{}}
	for _, rows := range []int64{1, 0} {
		mock.ExpectExec("(?s)INSERT INTO turn_action_recommendations.*ON CONFLICT DO NOTHING").WithArgs(int64(81), value.SessionID, "turn", "hash", "digest", sqlmock.AnyArg(), int64(123)).WillReturnResult(sqlmock.NewResult(0, rows))
		claimed, err := repo.ClaimTurnActionRecommendation(ctx, 81, "digest", value)
		require.NoError(t, err)
		require.Equal(t, rows == 1, claimed)
	}
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	mock.ExpectQuery("SELECT payload FROM turn_action_recommendations WHERE api_key_id = \\$1 AND session_id = \\$2 AND run_id = \\$3 AND context_id = \\$4").WithArgs(int64(81), value.SessionID, "turn", "hash").WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(payload))
	values, err := repo.ListTurnActionRecommendations(ctx, 81, value.SessionID, "turn", "hash")
	require.NoError(t, err)
	require.Len(t, values, 1)
	mock.ExpectExec("(?s)UPDATE turn_action_recommendations.*WHERE api_key_id = \\$1 AND session_id = \\$2 AND run_id = \\$3 AND context_id = \\$4 AND payload->>'state' = 'pending'").WithArgs(int64(81), value.SessionID, "turn", "hash", sqlmock.AnyArg(), int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.SaveTurnActionRecommendation(ctx, 81, value))
	require.NoError(t, mock.ExpectationsWereMet())
}
