package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) ClaimTurnActionRecommendation(ctx context.Context, keyID int64, digest string, value service.TurnActionRecommendation) (bool, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `INSERT INTO turn_action_recommendations
  (api_key_id, session_id, run_id, context_id, input_digest, payload, updated_at)
  VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
		keyID, value.SessionID, value.RunID, value.ContextID, digest, string(payload), value.UpdatedAt)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
func (r *usageLogRepository) SaveTurnActionRecommendation(ctx context.Context, keyID int64, value service.TurnActionRecommendation) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = r.sql.ExecContext(ctx, `UPDATE turn_action_recommendations SET payload = $5, updated_at = $6
  WHERE api_key_id = $1 AND session_id = $2 AND run_id = $3 AND context_id = $4 AND payload->>'state' = 'pending'`,
		keyID, value.SessionID, value.RunID, value.ContextID, string(payload), value.UpdatedAt)
	return err
}
func (r *usageLogRepository) TurnActionInputDigest(ctx context.Context, keyID int64, session, run, contextID string) (string, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT input_digest FROM turn_action_recommendations WHERE api_key_id = $1 AND session_id = $2 AND run_id = $3 AND context_id = $4`, keyID, session, run, contextID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var digest string
	if rows.Next() {
		if err := rows.Scan(&digest); err != nil {
			return "", err
		}
	}
	return digest, rows.Err()
}
func (r *usageLogRepository) ListTurnActionRecommendations(ctx context.Context, keyID int64, session, run, contextID string) ([]service.TurnActionRecommendation, error) {
	args := []any{keyID, session, run}
	filter := "api_key_id = $1 AND session_id = $2 AND run_id = $3"
	if contextID != "" {
		args = append(args, contextID)
		filter += " AND context_id = $4"
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT payload FROM turn_action_recommendations WHERE `+filter+` ORDER BY updated_at DESC, context_id DESC LIMIT 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []service.TurnActionRecommendation{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var value service.TurnActionRecommendation
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
