package repository

import (
	"context"
	"strings"
	"time"
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
				WHEN EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
				ELSE account_model_health.source
			END`, accountID, model, checkedAt)
	return err
}
