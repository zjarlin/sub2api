-- v0 (Vercel) 应用生成平台：扩展用户平台配额与组合路由约束，保留既有平台。
ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'madao', 'arena', 'minimax', 'cursor', 'windsurf', 'teleagent', 'opencode_go', 'kilo', 'doubao', 'traework', 'workbuddy', 'vibex', 'zcode', 'laya', 'jev', 'systemone', 'qoder', 'model3d', 'v0'));

ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'madao', 'arena', 'minimax', 'cursor', 'windsurf', 'teleagent', 'opencode_go', 'kilo', 'doubao', 'traework', 'workbuddy', 'vibex', 'zcode', 'laya', 'jev', 'systemone', 'qoder', 'model3d', 'v0'));
