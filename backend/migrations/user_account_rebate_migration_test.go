package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUserAccountRebateMigration 校验 268 号迁移建立结算游标与资金流水表，
// 并带幂等所需的唯一索引（同账号同窗口只允许一条流水）。
//
// 缺失唯一索引时，重复结算会把同一窗口的返额重复入账（直接多发余额）；
// 缺失游标表时，结算窗口无法推进，每轮都会从头重算。
func TestUserAccountRebateMigration(t *testing.T) {
	content, err := FS.ReadFile("268_user_account_rebate.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 结算游标表
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_account_rebate_cursors")
	require.Contains(t, sql, "settled_through TIMESTAMPTZ NOT NULL")
	require.Contains(t, sql, "owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE")

	// 资金流水表
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_account_rebate_ledger")
	require.Contains(t, sql, "window_from TIMESTAMPTZ NOT NULL")
	require.Contains(t, sql, "window_to TIMESTAMPTZ NOT NULL")
	require.Contains(t, sql, "consumed_basis DECIMAL(20,10) NOT NULL")
	require.Contains(t, sql, "rate_percent DECIMAL(10,4) NOT NULL")
	require.Contains(t, sql, "rebate_amount DECIMAL(20,10) NOT NULL")
	require.Contains(t, sql, "balance_after DECIMAL(20,10) NULL")

	// 幂等：同账号同窗口起点唯一
	require.Contains(t, sql,
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_user_account_rebate_ledger_window ON user_account_rebate_ledger(account_id, window_from)")

	// 查询索引
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_user_account_rebate_cursors_owner ON user_account_rebate_cursors(owner_user_id)")
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_user_account_rebate_ledger_owner ON user_account_rebate_ledger(owner_user_id, created_at DESC)")
}

// TestUserAccountRebateMigrationIsIdempotentFriendly 迁移文件必须全部使用
// IF NOT EXISTS，保证重复执行不报错（迁移器按 checksum 记录，但手工重跑
// 或半途失败重试时不应对既有对象报错）。
func TestUserAccountRebateMigrationIsIdempotentFriendly(t *testing.T) {
	content, err := FS.ReadFile("268_user_account_rebate.sql")
	require.NoError(t, err)
	sql := string(content)

	for _, stmt := range []string{"CREATE TABLE", "CREATE INDEX", "CREATE UNIQUE INDEX"} {
		idx := 0
		for {
			pos := strings.Index(sql[idx:], stmt)
			if pos < 0 {
				break
			}
			abs := idx + pos
			tail := sql[abs:]
			require.True(t, strings.HasPrefix(tail, stmt+" IF NOT EXISTS"),
				"statement %q must use IF NOT EXISTS: %s", stmt, firstLine(tail))
			idx = abs + len(stmt)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
