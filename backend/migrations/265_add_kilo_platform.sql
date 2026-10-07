-- Kilo AI 公共网关作为独立平台（免费池无鉴权，仅 Chat Completions），
-- 与 OpenCode 免费模式并列。仅扩展 CHECK 约束，不自动生成监控或账号。
ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'arena', 'minimax', 'cursor', 'windsurf', 'opencode_go', 'kilo', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex', 'systemone'));

ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'arena', 'minimax', 'cursor', 'windsurf', 'opencode_go', 'kilo', 'doubao', 'traework', 'workbuddy', 'zcode', 'laya', 'jev', 'vibex', 'systemone'));
