package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) RecordAccountModelHealthSuccess(
	ctx context.Context,
	accountID int64,
	model string,
	checkedAt time.Time,
) error {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" {
		return nil
	}
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO account_model_health (account_id, model, last_success_at, source)
		VALUES ($1, $2, $3, 'account_test')
		ON CONFLICT (account_id, model) DO UPDATE
		SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
			source = CASE
				WHEN account_model_health.last_success_at IS NULL OR EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
				ELSE account_model_health.source
			END`, accountID, model, checkedAt)
	return err
}

func (r *accountRepository) RecordAccountModelHealthFailure(
	ctx context.Context,
	accountID int64,
	model string,
	checkedAt time.Time,
) error {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" {
		return nil
	}
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO account_model_health (account_id, model, last_success_at, last_failure_at, source)
		VALUES ($1, $2, NULL, $3, 'account_test')
		ON CONFLICT (account_id, model) DO UPDATE
		SET last_failure_at = GREATEST(COALESCE(account_model_health.last_failure_at, EXCLUDED.last_failure_at), EXCLUDED.last_failure_at)`,
		accountID, model, checkedAt)
	return err
}

func (r *accountRepository) ListAccountModelHealthStates(ctx context.Context) ([]service.AccountModelHealthState, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT account_id, model, last_success_at, last_failure_at
		FROM account_model_health
		ORDER BY account_id, model`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	states := make([]service.AccountModelHealthState, 0)
	for rows.Next() {
		var state service.AccountModelHealthState
		var successAt, failureAt sql.NullTime
		if err := rows.Scan(&state.AccountID, &state.Model, &successAt, &failureAt); err != nil {
			return nil, err
		}
		if successAt.Valid {
			state.LastSuccessAt = &successAt.Time
		}
		if failureAt.Valid {
			state.LastFailureAt = &failureAt.Time
		}
		states = append(states, state)
	}
	return states, rows.Err()
}
