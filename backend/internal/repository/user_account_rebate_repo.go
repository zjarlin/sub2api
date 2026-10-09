package repository

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// userAccountRebateRepository 结算"用户自带账号按真实消耗返额"。
//
// 结算基数在 SQL 里取 LEAST(倍率前成本, 用户实付)：
//   - COALESCE(account_stats_cost, total_cost) 是账号口径成本，但号主可以自行
//     调高账号计费倍率把 total_cost 抬高（accounts.rate_multiplier 由号主可写），
//     所以倍率相关的量只能作为“上限的候选”，不能单独作为返额基数；
//   - actual_cost 是该请求真实从用户身上扣掉的钱。
//
// 两者取小后，返额 ≤ 实收 × 比例（比例封顶 100%），平台不会因返额净亏，
// 也堵死“开小号互相刷量套返额”的路径。
type userAccountRebateRepository struct {
	client *dbent.Client
}

func NewUserAccountRebateRepository(client *dbent.Client, _ *sql.DB) service.UserAccountRebateRepository {
	return &userAccountRebateRepository{client: client}
}

var _ service.UserAccountRebateRepository = (*userAccountRebateRepository)(nil)

// ListCandidateAccounts 返回参与返额结算的自带账号（号主非空、未删除）。
// requireShared 为 true 时只统计开启了公共调度共享的账号。
func (r *userAccountRebateRepository) ListCandidateAccounts(ctx context.Context, requireShared bool, limit int) ([]service.AccountRebateCandidate, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `
SELECT a.id, a.owner_user_id
FROM accounts a
WHERE a.deleted_at IS NULL
  AND a.owner_user_id IS NOT NULL
  AND ($1 = false OR COALESCE(a.extra->>'shared_for_public_scheduling', '') = 'true')
ORDER BY a.id
LIMIT $2`, requireShared, limit)
	if err != nil {
		return nil, fmt.Errorf("list account rebate candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	candidates := make([]service.AccountRebateCandidate, 0)
	for rows.Next() {
		var item service.AccountRebateCandidate
		if scanErr := rows.Scan(&item.AccountID, &item.OwnerUserID); scanErr != nil {
			return nil, scanErr
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

// ListRebateHistory 返回某个号主的返额流水（倒序）。
func (r *userAccountRebateRepository) ListRebateHistory(ctx context.Context, ownerUserID int64, limit, offset int) ([]service.AccountRebateRecord, int64, error) {
	client := clientFromContext(ctx, r.client)

	var total int64
	if err := scanSingleRow(ctx, client,
		`SELECT COUNT(*) FROM user_account_rebate_ledger WHERE owner_user_id = $1`,
		[]any{ownerUserID}, &total); err != nil {
		return nil, 0, err
	}

	rows, err := client.QueryContext(ctx, `
SELECT l.id, l.account_id, COALESCE(a.name, ''), l.window_from, l.window_to,
       l.requests, l.consumed_basis::double precision, l.rate_percent::double precision,
       l.rebate_amount::double precision, l.balance_after::double precision, l.created_at
FROM user_account_rebate_ledger l
LEFT JOIN accounts a ON a.id = l.account_id
WHERE l.owner_user_id = $1
ORDER BY l.created_at DESC, l.id DESC
LIMIT $2 OFFSET $3`, ownerUserID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	return scanAccountRebateRecords(rows, total)
}

// ListAllRebateRecords 返回全部返额流水（管理端）。
func (r *userAccountRebateRepository) ListAllRebateRecords(ctx context.Context, search string, limit, offset int) ([]service.AccountRebateRecord, int64, error) {
	client := clientFromContext(ctx, r.client)

	where := ""
	args := []any{}
	if search != "" {
		where = `WHERE u.email ILIKE $1 OR u.username ILIKE $1 OR l.account_id::text = $2`
		args = append(args, "%"+search+"%", search)
	}
	// 计数与列表共用同一 where，参数编号需要重排。
	countArgs := append([]any{}, args...)
	var total int64
	if err := scanSingleRow(ctx, client,
		`SELECT COUNT(*) FROM user_account_rebate_ledger l JOIN users u ON u.id = l.owner_user_id `+where,
		countArgs, &total); err != nil {
		return nil, 0, err
	}

	next := len(args) + 1
	args = append(args, limit, offset)
	rows, err := client.QueryContext(ctx, fmt.Sprintf(`
SELECT l.id, l.account_id, COALESCE(a.name, ''), l.window_from, l.window_to,
       l.requests, l.consumed_basis::double precision, l.rate_percent::double precision,
       l.rebate_amount::double precision, l.balance_after::double precision, l.created_at,
       COALESCE(u.email, ''), COALESCE(u.username, '')
FROM user_account_rebate_ledger l
JOIN users u ON u.id = l.owner_user_id
LEFT JOIN accounts a ON a.id = l.account_id
%s
ORDER BY l.created_at DESC, l.id DESC
LIMIT $%d OFFSET $%d`, where, next, next+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	return scanAccountRebateRecordsWithUser(rows, total)
}

func scanAccountRebateRecords(rows *sql.Rows, total int64) ([]service.AccountRebateRecord, int64, error) {
	items := make([]service.AccountRebateRecord, 0)
	for rows.Next() {
		var item service.AccountRebateRecord
		var balanceAfter sql.NullFloat64
		if err := rows.Scan(
			&item.ID, &item.AccountID, &item.AccountName, &item.WindowFrom, &item.WindowTo,
			&item.Requests, &item.ConsumedBasis, &item.RatePercent,
			&item.RebateAmount, &balanceAfter, &item.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		if balanceAfter.Valid {
			value := balanceAfter.Float64
			item.BalanceAfter = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func scanAccountRebateRecordsWithUser(rows *sql.Rows, total int64) ([]service.AccountRebateRecord, int64, error) {
	items := make([]service.AccountRebateRecord, 0)
	for rows.Next() {
		var item service.AccountRebateRecord
		var balanceAfter sql.NullFloat64
		if err := rows.Scan(
			&item.ID, &item.AccountID, &item.AccountName, &item.WindowFrom, &item.WindowTo,
			&item.Requests, &item.ConsumedBasis, &item.RatePercent,
			&item.RebateAmount, &balanceAfter, &item.CreatedAt,
			&item.OwnerEmail, &item.OwnerUsername,
		); err != nil {
			return nil, 0, err
		}
		if balanceAfter.Valid {
			value := balanceAfter.Float64
			item.BalanceAfter = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetRebateSummary 返回号主的累计返额概览。
func (r *userAccountRebateRepository) GetRebateSummary(ctx context.Context, ownerUserID int64) (*service.AccountRebateSummary, error) {
	summary := &service.AccountRebateSummary{OwnerUserID: ownerUserID}
	if err := scanSingleRow(ctx, clientFromContext(ctx, r.client), `
SELECT COALESCE(SUM(l.rebate_amount), 0)::double precision,
       COALESCE(SUM(l.consumed_basis), 0)::double precision,
       COUNT(*)::bigint
FROM user_account_rebate_ledger l
WHERE l.owner_user_id = $1`, []any{ownerUserID},
		&summary.TotalRebated, &summary.TotalConsumedBasis, &summary.SettleCount); err != nil {
		return nil, err
	}
	if err := scanSingleRow(ctx, clientFromContext(ctx, r.client), `
SELECT COUNT(*)::bigint
FROM accounts a
WHERE a.deleted_at IS NULL AND a.owner_user_id = $1`, []any{ownerUserID},
		&summary.OwnedAccountCount); err != nil {
		return nil, err
	}
	return summary, nil
}

// SettleAccount 在单个事务里结算一个账号的一个窗口。
//
// 事务内先对结算游标行加 FOR UPDATE 锁，再做聚合、入账、写流水、推进游标，
// 保证同一账号不会被并发结算两次。幂等由两层保证：
//  1. 游标行锁 + settled_through 单调推进；
//  2. user_account_rebate_ledger(account_id, window_from) 唯一索引，冲突即整轮回滚。
func (r *userAccountRebateRepository) SettleAccount(ctx context.Context, in service.AccountRebateSettleInput) (*service.AccountRebateSettlement, error) {
	result := &service.AccountRebateSettlement{
		AccountID:   in.AccountID,
		OwnerUserID: in.OwnerUserID,
		RatePercent: in.RatePercent,
	}

	err := r.withTx(ctx, func(txCtx context.Context, txClient *dbent.Client) error {
		// 1) 确保游标存在，再取行锁。
		if _, err := txClient.ExecContext(txCtx, `
INSERT INTO user_account_rebate_cursors (account_id, owner_user_id, settled_through, created_at, updated_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (account_id) DO NOTHING`, in.AccountID, in.OwnerUserID, in.WindowTo); err != nil {
			return fmt.Errorf("ensure rebate cursor: %w", err)
		}

		var (
			ownerUserID    int64
			settledThrough time.Time
			totalConsumed  float64
			totalRebated   float64
		)
		if err := scanSingleRow(txCtx, txClient, `
SELECT owner_user_id, settled_through, total_consumed::double precision, total_rebated::double precision
FROM user_account_rebate_cursors
WHERE account_id = $1
FOR UPDATE`, []any{in.AccountID},
			&ownerUserID, &settledThrough, &totalConsumed, &totalRebated); err != nil {
			return fmt.Errorf("lock rebate cursor: %w", err)
		}

		result.OwnerUserID = ownerUserID
		result.WindowFrom = settledThrough
		result.WindowTo = in.WindowTo

		// 2) 窗口为空则直接推进游标（账号停用/无流量时也要前移，避免反复扫描老范围）。
		if !in.WindowTo.After(settledThrough) {
			result.Skipped = true
			return nil
		}

		// 3) 聚合窗口用量。默认排除号主自己的消费（只结算"别人用的量"）。
		args := []any{in.AccountID, settledThrough, in.WindowTo}
		excludeOwner := ""
		if !in.IncludeOwnerUsage {
			excludeOwner = " AND user_id <> $4"
			args = append(args, ownerUserID)
		}
		var requests int64
		var basis float64
		if err := scanSingleRow(txCtx, txClient, `
SELECT COUNT(*)::bigint,
       COALESCE(SUM(LEAST(COALESCE(account_stats_cost, total_cost), actual_cost)), 0)::double precision
FROM usage_logs
WHERE account_id = $1 AND created_at >= $2 AND created_at < $3`+excludeOwner, args,
			&requests, &basis); err != nil {
			return fmt.Errorf("aggregate rebate window: %w", err)
		}

		result.Requests = requests
		result.ConsumedBasis = basis
		if basis <= 0 {
			result.RebateAmount = 0
		} else {
			result.RebateAmount = roundRebateAmount(basis * in.RatePercent / 100)
		}

		// 4) 入账（返额 <= 实收，恒为正，不涉及余额不足分支）。
		if result.RebateAmount > 0 {
			var balanceAfter float64
			if err := scanSingleRow(txCtx, txClient, `
UPDATE users
SET balance = balance + $1, updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL
RETURNING balance::double precision`, []any{result.RebateAmount, ownerUserID},
				&balanceAfter); err != nil {
				return fmt.Errorf("credit rebate balance: %w", err)
			}
			result.BalanceAfter = &balanceAfter

			if _, err := txClient.ExecContext(txCtx, `
INSERT INTO user_account_rebate_ledger
    (account_id, owner_user_id, window_from, window_to, requests, consumed_basis, rate_percent, rebate_amount, balance_after, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
				in.AccountID, ownerUserID, settledThrough, in.WindowTo, requests, basis,
				in.RatePercent, result.RebateAmount, balanceAfter); err != nil {
				return fmt.Errorf("insert rebate ledger: %w", err)
			}
		}

		// 5) 推进游标。无返额（basis<=0）也必须前进，否则下轮重扫同一窗口。
		if _, err := txClient.ExecContext(txCtx, `
UPDATE user_account_rebate_cursors
SET settled_through = $2,
    total_consumed = total_consumed + $3,
    total_rebated = total_rebated + $4,
    owner_user_id = $5,
    updated_at = NOW()
WHERE account_id = $1`,
			in.AccountID, in.WindowTo, basis, result.RebateAmount, ownerUserID); err != nil {
			return fmt.Errorf("advance rebate cursor: %w", err)
		}

		result.Settled = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *userAccountRebateRepository) withTx(ctx context.Context, fn func(txCtx context.Context, txClient *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin account rebate transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, tx.Client()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account rebate transaction: %w", err)
	}
	return nil
}

// roundRebateAmount 把返额金额量化到 8 位小数，避免浮点尾差进入余额。
func roundRebateAmount(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0
	}
	return math.Round(value*1e8) / 1e8
}
