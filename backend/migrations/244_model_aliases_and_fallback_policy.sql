-- 模型同义词和自动降级是运行时功能配置，随源码版本发布。
-- 仅在目标键不存在时写入默认值，保留管理员已经调整过的配置。
INSERT INTO settings (key, value, updated_at)
VALUES
    (
        'model_aliases',
        '{"groups":[{"canonical":"deepseek-v4-flash","aliases":["DeepSeek-V4-Flash","cn:deepseek-v4-flash"]},{"canonical":"deepseek-v4-pro","aliases":["DeepSeek-V4-Pro","DeepSeek-V4-Pro-Official","cn:deepseek-v4-pro"]},{"canonical":"deepseek-v4.1-flash","aliases":["cn:deepseek-v4.1-flash"]},{"canonical":"glm-5.3","aliases":["cn:glm-5.3"]},{"canonical":"glm-5.2","aliases":["cn:glm-5.2"]},{"canonical":"glm-5.3-flash","aliases":["cn:glm-5.3-flash"]},{"canonical":"kimi-k3","aliases":["cn:kimi-k3-1"]},{"canonical":"kimi-k2.6","aliases":["cn:kimi-k2.6"]},{"canonical":"minimax-m3","aliases":["cn:minimax-m3"]}]}', NOW()),
    (
        'model_fallback_policy',
        '{"enabled":true,"tiers":[{"name":"旗舰","models":["deepseek-v4-pro","gpt-5.6-terra"]},{"name":"高能力","models":["deepseek-v4-flash","deepseek-v4.1-flash","glm-5.3","glm-5.3-flash","glm-5.2","gpt-5.3-codex","kimi-k3"]},{"name":"中等","models":["cn:deepseek-v3-0324","cn:deepseek-v3-0324-lkeap","cn:deepseek-v3-1-lkeap","cn:deepseek-v3-2-volc","kimi-k2.6","qwen3.8-27b","minimax-m3"]},{"name":"备用","models":["cn:glm-5.1","cn:glm-5.0-turbo","cn:hunyuan-2.0-instruct","cn:hunyuan-chat","cn:hy4-preview","cn:hy4-preview-f","cn:hy3","cn:hy3-x","cn:kimi-k2.5","cn:kimi-k2.8-preview","cn:minimax-m2.7","gemini-3.5-flash","grok-4.5","doubao-pro"]}]}', NOW()),
    (
        'model_fallback_policy',
        '{"enabled":true,"tiers":[{"name":"旗舰","models":["deepseek-v4-pro","gpt-5.6-terra"]},{"name":"高能力","models":["deepseek-v4-flash","deepseek-v4.1-flash","glm-5.3","glm-5.3-flash","glm-5.2","gpt-5.3-codex","kimi-k3"]},{"name":"中等","models":["cn:deepseek-v3-0324","cn:deepseek-v3-0324-lkeap","cn:deepseek-v3-1-lkeap","cn:deepseek-v3-2-volc","kimi-k2.6","qwen3.8-27b","minimax-m3"]},{"name":"备用","models":["cn:glm-5.1","cn:glm-5.0-turbo","cn:hunyuan-2.0-instruct","cn:hunyuan-chat","cn:hy4-preview","cn:hy4-preview-f","cn:hy3","cn:hy3-x","cn:kimi-k2.5","cn:kimi-k2.8-preview","cn:minimax-m2.7","gemini-3.5-flash","grok-4.5","doubao-pro"]}]}', NOW()),
    (
        'vision_fallback_policy',
        '{"enabled":true,"models":[],"allow_unlisted_models":true,"candidate_timeout_seconds":15,"timeout_seconds":45}', NOW())
ON CONFLICT (key) DO NOTHING;
