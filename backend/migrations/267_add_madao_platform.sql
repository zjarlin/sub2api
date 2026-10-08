-- 码道（华为云 CodeArts 代码智能体 Web 端）作为独立平台，走内置适配器与网页登录，
-- 与官方华为云模型 API 平台区分。仅扩展 CHECK 约束，不自动生成监控或账号。
ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'madao', 'arena', 'minimax', 'cursor', 'windsurf', 'opencode_go', 'kilo', 'doubao', 'traework', 'workbuddy', 'vibex', 'zcode', 'laya', 'jev', 'systemone'));

ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'madao', 'arena', 'minimax', 'cursor', 'windsurf', 'opencode_go', 'kilo', 'doubao', 'traework', 'workbuddy', 'vibex', 'zcode', 'laya', 'jev', 'systemone'));
