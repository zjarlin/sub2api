-- Arena 使用独立平台配额和 Composite 文本路由；不启用自动生成监控。
ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'arena', 'minimax', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex'));

ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'arena', 'minimax', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex'));
