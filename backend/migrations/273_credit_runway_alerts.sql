-- 使用现有运维通知渠道；没有相应账号的平台不会产生低余额告警。
-- 阈值为预计剩余小时，避免不同平台的积分/货币单位被直接比较。
INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    filters, created_at, updated_at
)
SELECT
    platform || CASE WHEN hours = 24 THEN ' 积分/余额不足一天' ELSE ' 积分/余额即将耗尽' END,
    '根据真实积分或余额的消耗速度和加速度预测；正常每小时检查，动态调整至15分钟～6小时，充值或签到增长后重置趋势。',
    true, 'credit_runway_hours', '<=', hours,
    1, 1, severity, true, 360,
    jsonb_build_object('platform', platform), NOW(), NOW()
FROM (VALUES ('workbuddy'), ('traework'), ('kimi'), ('deepseek')) AS platforms(platform)
CROSS JOIN (VALUES (24, 'P2'), (6, 'P1')) AS levels(hours, severity)
ON CONFLICT (name) DO NOTHING;
