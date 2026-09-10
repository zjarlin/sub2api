package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) ListModelHealthObservations(
	ctx context.Context,
	groupID *int64,
	platform string,
) ([]service.ModelHealthObservation, error) {
	query := `
		SELECT h.account_id, h.model, h.last_success_at
		FROM account_model_health h
		JOIN accounts a ON a.id = h.account_id AND a.deleted_at IS NULL
		WHERE h.model <> ''`
	args := make([]any, 0, 2)
	if platform = strings.TrimSpace(platform); platform != "" {
		args = append(args, platform)
		query += fmt.Sprintf(" AND a.platform = $%d", len(args))
	}
	if groupID != nil && *groupID > 0 {
		args = append(args, *groupID)
		query += fmt.Sprintf(`
			AND EXISTS (
				SELECT 1 FROM account_groups ag
				WHERE ag.account_id = h.account_id AND ag.group_id = $%d
			)`, len(args))
	}
	query += " ORDER BY h.model, h.account_id"

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	observations := make([]service.ModelHealthObservation, 0)
	for rows.Next() {
		var observation service.ModelHealthObservation
		if err := rows.Scan(&observation.AccountID, &observation.Model, &observation.CheckedAt); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}
