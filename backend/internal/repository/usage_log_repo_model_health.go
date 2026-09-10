package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) ListRecentModelHealthObservations(
	ctx context.Context,
	groupID *int64,
	platform string,
	since time.Time,
) ([]service.ModelHealthObservation, error) {
	query := `
		WITH observations AS (
			SELECT ul.account_id,
			       COALESCE(NULLIF(BTRIM(ul.requested_model), ''), BTRIM(ul.model)) AS model,
			       ul.created_at AS checked_at
			FROM usage_logs ul
			WHERE ul.created_at >= $1
			  AND ` + usageLogSuccessFilterUL + `
			UNION ALL
			SELECT p.account_id, BTRIM(p.model_id) AS model, latest.finished_at AS checked_at
			FROM scheduled_test_plans p
			JOIN LATERAL (
				SELECT r.status, r.finished_at
				FROM scheduled_test_results r
				WHERE r.plan_id = p.id
				ORDER BY r.finished_at DESC
				LIMIT 1
			) latest ON latest.status = 'success' AND latest.finished_at >= $1
		)
		SELECT o.account_id, o.model, MAX(o.checked_at)
		FROM observations o
		JOIN accounts a ON a.id = o.account_id AND a.deleted_at IS NULL
		WHERE o.model <> ''`
	args := []any{since}
	if platform = strings.TrimSpace(platform); platform != "" {
		args = append(args, platform)
		query += fmt.Sprintf(" AND a.platform = $%d", len(args))
	}
	if groupID != nil && *groupID > 0 {
		args = append(args, *groupID)
		query += fmt.Sprintf(`
			AND EXISTS (
				SELECT 1 FROM account_groups ag
				WHERE ag.account_id = o.account_id AND ag.group_id = $%d
			)`, len(args))
	}
	query += " GROUP BY o.account_id, o.model ORDER BY o.model, o.account_id"

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
