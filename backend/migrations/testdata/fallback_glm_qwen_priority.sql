-- 用法：psql -X -v ON_ERROR_STOP=1 -f migrations/testdata/fallback_glm_qwen_priority.sql
-- 仅操作会话临时表，结束时回滚。
\set ON_ERROR_STOP on
BEGIN;
CREATE TEMP TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
) ON COMMIT DROP;
\ir ../244_model_aliases_and_fallback_policy.sql
UPDATE settings SET value = '{"enabled":true,"tiers":[
    {"name":"旗舰","models":["gpt-6-astra","gpt-5.6-sol"]},
    {"name":"主力编码","models":["deepseek-v4.1-flash","deepseek-v4-pro","deepseek-v4-flash","gpt-5.6-terra","glm-5.3","glm-5.3-flash","kimi-k3","qwen3.8-max","grok-4.7"]},
    {"name":"通用编码","models":["glm-5.2","qwen3.7-max","minimax-m3"]}
]}' WHERE key = 'model_fallback_policy';
CREATE TEMP TABLE initial_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../262_prioritize_glm_qwen_fallback.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_fallback_policy');
    main_models JSONB := p->'tiers'->1->'models';
BEGIN
    IF main_models <> '["deepseek-v4.1-flash","glm-5.3","glm-5.3-flash","qwen3.8-max","deepseek-v4-pro","deepseek-v4-flash","kimi-k3","grok-4.7","gpt-5.6-terra"]'::jsonb THEN
        RAISE EXCEPTION '主力编码档位顺序不符合 GLM/Qwen 优先和 Terra 后置：%', main_models;
    END IF;
    IF p->'tiers'->2->'models' <> '["glm-5.2","qwen3.7-max","minimax-m3"]'::jsonb THEN
        RAISE EXCEPTION '不能改变其他档位';
    END IF;
    IF EXISTS (SELECT 1 FROM settings s JOIN initial_settings i USING (key)
        WHERE s.key <> 'model_fallback_policy' AND s.value IS DISTINCT FROM i.value) THEN
        RAISE EXCEPTION '不能改变其他配置';
    END IF;
END $$;

CREATE TEMP TABLE after_first_run ON COMMIT DROP AS SELECT * FROM settings;
\ir ../262_prioritize_glm_qwen_fallback.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持配置和更新时间不变';
    END IF;
END $$;

UPDATE settings SET value = '{"enabled":true,"tiers":[
    {"name":"custom","models":["deepseek-v4-pro","glm-5.3","gpt-5.6-terra"]}
]}' WHERE key = 'model_fallback_policy';
CREATE TEMP TABLE custom_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../262_prioritize_glm_qwen_fallback.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM custom_settings) THEN
        RAISE EXCEPTION '缺少 DeepSeek V4.1 Flash 时不能改写自定义策略';
    END IF;
END $$;

ROLLBACK;
\echo 'Fallback GLM/Qwen priority migration checks passed'
