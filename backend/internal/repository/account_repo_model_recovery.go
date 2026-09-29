package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 只清除同一账号凭据、代理和失败记录下已恢复的模型；并发新增失败与其他模型均保留。
func (r *accountRepository) ClearUnsupportedModelIfObserved(ctx context.Context, account *service.Account, model string, observed any) (bool, error) {
	model = strings.ToLower(strings.TrimSpace(model))
	if account == nil || account.ID <= 0 || model == "" {
		return false, nil
	}
	if r == nil || r.sql == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	observationJSON, err := json.Marshal(observed)
	if err != nil {
		return false, err
	}
	credentialsJSON, err := json.Marshal(normalizeJSONMap(account.Credentials))
	if err != nil {
		return false, err
	}
	// 恢复和调度通知在同一语句内提交，比较失败时两者均不写入。
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
			UPDATE accounts AS a
			SET extra = a.extra #- ARRAY['unsupported_models', $1]::text[], updated_at = NOW()
			WHERE a.id = $2
				AND a.deleted_at IS NULL
				AND a.platform = $3
				AND a.type = $4
				AND a.credentials = $5::jsonb
				AND a.proxy_id IS NOT DISTINCT FROM $6
				AND a.extra->'unsupported_models'->$1 = $7::jsonb
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $8, updated.id, NULL, NULL FROM updated
	`, model, account.ID, account.Platform, account.Type, string(credentialsJSON), account.ProxyID,
		string(observationJSON), service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, account.ID)
	return true, nil
}
