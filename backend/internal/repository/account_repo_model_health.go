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
		WITH health AS (INSERT INTO account_model_health (account_id, model, last_success_at, source)
		VALUES ($1, $2, $3, 'account_test')
		ON CONFLICT (account_id, model) DO UPDATE
		SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
			source = CASE
				WHEN account_model_health.last_success_at IS NULL OR EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
				ELSE account_model_health.source
			END
		RETURNING account_id, model, last_success_at, last_failure_at),
		changed AS (
			UPDATE accounts a SET extra = jsonb_set(COALESCE(a.extra, '{}'::jsonb),
				'{verified_upstream_models}', COALESCE(a.extra->'verified_upstream_models', '{}'::jsonb) ||
				jsonb_build_object(LOWER(BTRIM(h.model)), h.last_success_at)), updated_at = CURRENT_TIMESTAMP
			FROM health h WHERE a.id = h.account_id AND a.deleted_at IS NULL
				AND (h.last_failure_at IS NULL OR h.last_success_at > h.last_failure_at)
			RETURNING a.id)
		INSERT INTO scheduler_outbox(event_type, account_id, payload)
		SELECT 'account_changed', id, '{}'::jsonb FROM changed`, accountID, model, checkedAt)
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
		WITH health AS (INSERT INTO account_model_health (account_id, model, last_success_at, last_failure_at, source)
		VALUES ($1, $2, NULL, $3, 'account_test')
		ON CONFLICT (account_id, model) DO UPDATE
		SET last_failure_at = GREATEST(COALESCE(account_model_health.last_failure_at, EXCLUDED.last_failure_at), EXCLUDED.last_failure_at)
		RETURNING account_id, model, last_success_at, last_failure_at),
		changed AS (
			UPDATE accounts a SET extra = jsonb_set(a.extra, '{verified_upstream_models}',
				(a.extra->'verified_upstream_models') - LOWER(BTRIM(h.model))), updated_at = CURRENT_TIMESTAMP
			FROM health h WHERE a.id = h.account_id AND a.deleted_at IS NULL
				AND a.extra->'verified_upstream_models' ? LOWER(BTRIM(h.model))
				AND (h.last_success_at IS NULL OR h.last_failure_at >= h.last_success_at)
			RETURNING a.id)
		INSERT INTO scheduler_outbox(event_type, account_id, payload)
		SELECT 'account_changed', id, '{}'::jsonb FROM changed`,
		accountID, model, checkedAt)
	return err
}

func (r *accountRepository) ListAccountModelHealthStates(ctx context.Context) ([]service.AccountModelHealthState, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT account_id, model, last_success_at, last_failure_at, last_probe_at
		FROM account_model_health
		ORDER BY account_id, model`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	states := make([]service.AccountModelHealthState, 0)
	for rows.Next() {
		var state service.AccountModelHealthState
		var successAt, failureAt, probeAt sql.NullTime
		if err := rows.Scan(&state.AccountID, &state.Model, &successAt, &failureAt, &probeAt); err != nil {
			return nil, err
		}
		if successAt.Valid {
			state.LastSuccessAt = &successAt.Time
		}
		if failureAt.Valid {
			state.LastFailureAt = &failureAt.Time
		}
		if probeAt.Valid {
			state.LastProbeAt = &probeAt.Time
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// 使用数据库时间原子占用模型，重启和多实例均共享冷却；占用不写入成功或失败结论。
func (r *accountRepository) ClaimAccountModelProbe(ctx context.Context, accountID int64, model string, interval time.Duration) (bool, error) {
	model = strings.ToLower(strings.TrimSpace(model))
	if accountID <= 0 || model == "" {
		return false, nil
	}
	if interval < service.MinimumModelHealthProbeInterval {
		interval = service.MinimumModelHealthProbeInterval
	}
	result, err := r.sql.ExecContext(ctx, `
		INSERT INTO account_model_health (account_id, model, last_success_at, last_probe_at, source)
		SELECT $1::bigint, $2::text, NULL, CURRENT_TIMESTAMP, 'automatic_probe'
		WHERE NOT EXISTS (
			SELECT 1 FROM account_model_health
			WHERE account_id = $1 AND LOWER(BTRIM(model)) = $2
			  AND GREATEST(last_success_at, last_failure_at, last_probe_at) > CURRENT_TIMESTAMP - $3::bigint * INTERVAL '1 microsecond'
		)
		ON CONFLICT (account_id, model) DO UPDATE
		SET last_probe_at = EXCLUDED.last_probe_at
		WHERE COALESCE(GREATEST(account_model_health.last_probe_at, account_model_health.last_success_at, account_model_health.last_failure_at), '-infinity'::timestamptz)
			<= EXCLUDED.last_probe_at - $3::bigint * INTERVAL '1 microsecond'`, accountID, model, interval.Microseconds())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}
