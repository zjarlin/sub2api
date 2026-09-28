-- DeepSeek 网页会话使用独立平台，避免复用官方 API Key 平台的额度与路由。
ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'minimax', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex'));

ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'minimax', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex'));

ALTER TABLE channel_monitors DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;
ALTER TABLE channel_monitors ADD CONSTRAINT channel_monitors_provider_check
    CHECK (provider IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'minimax', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex'));

ALTER TABLE channel_monitor_request_templates DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;
ALTER TABLE channel_monitor_request_templates ADD CONSTRAINT channel_monitor_request_templates_provider_check
    CHECK (provider IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'minimax', 'opencode_go', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex'));
