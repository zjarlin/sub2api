-- 用户自带账号"按真实消耗返额"：结算游标 + 资金流水。
--
-- 号主通过「我的账号」贡献的上游账号被平台真实消耗掉的用量，按配置比例折算成
-- 站内余额返还给号主。结算基数取「倍率前成本」与「用户实付」的较小值，因此
-- 无论号主如何调整账号计费倍率（rate_multiplier）或开小号互相消费，
-- 平台返出的余额都不会超过该账号带来的实收，返额本身不可能造成净亏。

-- 每个账号一条结算游标：settled_through 之前的使用日志已经结算完毕。
CREATE TABLE IF NOT EXISTS user_account_rebate_cursors (
    account_id      BIGINT PRIMARY KEY,
    owner_user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    settled_through TIMESTAMPTZ NOT NULL,
    total_consumed  DECIMAL(20,10) NOT NULL DEFAULT 0,
    total_rebated   DECIMAL(20,10) NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_account_rebate_cursors_owner
    ON user_account_rebate_cursors(owner_user_id);

-- account_id 不建外键：账号被硬删除后仍要保留已发生的结算流水供审计。
CREATE TABLE IF NOT EXISTS user_account_rebate_ledger (
    id             BIGSERIAL PRIMARY KEY,
    account_id     BIGINT NOT NULL,
    owner_user_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    window_from    TIMESTAMPTZ NOT NULL,
    window_to      TIMESTAMPTZ NOT NULL,
    requests       BIGINT NOT NULL DEFAULT 0,
    consumed_basis DECIMAL(20,10) NOT NULL,
    rate_percent   DECIMAL(10,4) NOT NULL,
    rebate_amount  DECIMAL(20,10) NOT NULL,
    balance_after  DECIMAL(20,10) NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 第二重幂等保险：同一账号同一窗口起点只允许一条流水，重复结算整轮回滚。
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_account_rebate_ledger_window
    ON user_account_rebate_ledger(account_id, window_from);

CREATE INDEX IF NOT EXISTS idx_user_account_rebate_ledger_owner
    ON user_account_rebate_ledger(owner_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_account_rebate_ledger_account
    ON user_account_rebate_ledger(account_id, created_at DESC);

COMMENT ON TABLE user_account_rebate_cursors IS '用户自带账号返额结算游标（每账号一条）';
COMMENT ON COLUMN user_account_rebate_cursors.settled_through IS '该时刻之前的使用日志已结算完毕；下次窗口从此刻开始';
COMMENT ON TABLE user_account_rebate_ledger IS '用户自带账号返额资金流水（按结算窗口）';
COMMENT ON COLUMN user_account_rebate_ledger.consumed_basis IS '结算基数 = SUM(LEAST(COALESCE(account_stats_cost,total_cost), actual_cost))，倍率前成本与用户实付取小';
COMMENT ON COLUMN user_account_rebate_ledger.rate_percent IS '本次结算生效的返额比例（百分比快照）';
