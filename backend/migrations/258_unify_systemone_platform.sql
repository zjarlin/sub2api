-- Laya 与 JEV 使用同一 System One wire protocol，统一为一个账号/额度/调度平台。
-- 原始上游归属保存在 credentials.systemone_provider，保留模型选择与 JEV -> Laya 回退能力。

UPDATE accounts
SET credentials = jsonb_set(
        COALESCE(credentials, '{}'::jsonb),
        '{systemone_provider}',
        to_jsonb(platform),
        true
    ),
    platform = 'systemone'
WHERE platform IN ('laya', 'jev');

-- 合并同一用户已有 Laya/JEV 配额：限额取更严格值，用量相加，窗口取较新的起点。
INSERT INTO user_platform_quotas (
    user_id, platform,
    daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
    daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
    daily_window_start, weekly_window_start, monthly_window_start,
    created_at, updated_at
)
SELECT user_id, 'systemone',
       CASE WHEN bool_or(daily_limit_usd IS NULL) THEN NULL ELSE min(daily_limit_usd) END,
       CASE WHEN bool_or(weekly_limit_usd IS NULL) THEN NULL ELSE min(weekly_limit_usd) END,
       CASE WHEN bool_or(monthly_limit_usd IS NULL) THEN NULL ELSE min(monthly_limit_usd) END,
       sum(daily_usage_usd), sum(weekly_usage_usd), sum(monthly_usage_usd),
       max(daily_window_start), max(weekly_window_start), max(monthly_window_start),
       min(created_at), max(updated_at)
FROM user_platform_quotas
WHERE platform IN ('laya', 'jev') AND deleted_at IS NULL
GROUP BY user_id
ON CONFLICT (user_id, platform) WHERE deleted_at IS NULL DO UPDATE
SET daily_limit_usd = CASE WHEN user_platform_quotas.daily_limit_usd IS NULL OR EXCLUDED.daily_limit_usd IS NULL THEN NULL ELSE LEAST(user_platform_quotas.daily_limit_usd, EXCLUDED.daily_limit_usd) END,
    weekly_limit_usd = CASE WHEN user_platform_quotas.weekly_limit_usd IS NULL OR EXCLUDED.weekly_limit_usd IS NULL THEN NULL ELSE LEAST(user_platform_quotas.weekly_limit_usd, EXCLUDED.weekly_limit_usd) END,
    monthly_limit_usd = CASE WHEN user_platform_quotas.monthly_limit_usd IS NULL OR EXCLUDED.monthly_limit_usd IS NULL THEN NULL ELSE LEAST(user_platform_quotas.monthly_limit_usd, EXCLUDED.monthly_limit_usd) END,
    daily_usage_usd = user_platform_quotas.daily_usage_usd + EXCLUDED.daily_usage_usd,
    weekly_usage_usd = user_platform_quotas.weekly_usage_usd + EXCLUDED.weekly_usage_usd,
    monthly_usage_usd = user_platform_quotas.monthly_usage_usd + EXCLUDED.monthly_usage_usd,
    daily_window_start = GREATEST(user_platform_quotas.daily_window_start, EXCLUDED.daily_window_start),
    weekly_window_start = GREATEST(user_platform_quotas.weekly_window_start, EXCLUDED.weekly_window_start),
    monthly_window_start = GREATEST(user_platform_quotas.monthly_window_start, EXCLUDED.monthly_window_start),
    updated_at = GREATEST(user_platform_quotas.updated_at, EXCLUDED.updated_at);

UPDATE user_platform_quotas
SET deleted_at = NOW(), updated_at = NOW()
WHERE platform IN ('laya', 'jev') AND deleted_at IS NULL;

ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'arena', 'minimax', 'cursor', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex', 'systemone'));

ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'arena', 'minimax', 'cursor', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex', 'systemone'));
