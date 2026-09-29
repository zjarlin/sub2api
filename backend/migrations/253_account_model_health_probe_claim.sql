-- 探测尝试独立于健康结论保存，失败、中断或结果写入故障也不会提前重复付费。
ALTER TABLE account_model_health
    ADD COLUMN IF NOT EXISTS last_probe_at TIMESTAMPTZ;

COMMENT ON COLUMN account_model_health.last_probe_at IS '自动模型探测原子占用时间，不代表探测成功或健康';
