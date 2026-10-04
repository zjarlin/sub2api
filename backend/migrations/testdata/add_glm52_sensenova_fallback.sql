-- 用法：psql -X -v ON_ERROR_STOP=1 -f migrations/testdata/add_glm52_sensenova_fallback.sql
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
    {"name":"通用编码","models":["gpt-5.6-luna","glm-5.2","qwen3.7-max","sensenova-6.8-flash-lite","minimax-m3"]},
    {"name":"备用","models":["doubao-pro"]}
]}' WHERE key = 'model_fallback_policy';
CREATE TEMP TABLE initial_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../263_add_glm52_sensenova_fallback.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_fallback_policy');
BEGIN
    IF p->'tiers'->1->'models' <> '["gpt-5.6-luna","glm-5.2","sensenova-6.8-flash-lite","qwen3.7-max","minimax-m3"]'::jsonb THEN
        RAISE EXCEPTION 'GLM-5.2 与 SenseNova 未形成连续降级路线：%', p->'tiers'->1->'models';
    END IF;
    IF p->'tiers'->2->'models' <> '["doubao-pro"]'::jsonb THEN
        RAISE EXCEPTION '不能改变其他档位';
    END IF;
    IF EXISTS (SELECT 1 FROM settings s JOIN initial_settings i USING (key)
        WHERE s.key <> 'model_fallback_policy' AND s.value IS DISTINCT FROM i.value) THEN
        RAISE EXCEPTION '不能改变其他配置';
    END IF;
END $$;

CREATE TEMP TABLE after_first_run ON COMMIT DROP AS SELECT * FROM settings;
\ir ../263_add_glm52_sensenova_fallback.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持配置和更新时间不变';
    END IF;
END $$;

UPDATE settings SET value = '{"enabled":true,"tiers":[
    {"name":"custom","models":["custom-model","qwen3.7-max","minimax-m3"]}
]}' WHERE key = 'model_fallback_policy';
CREATE TEMP TABLE custom_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../263_add_glm52_sensenova_fallback.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM custom_settings) THEN
        RAISE EXCEPTION '缺少 GLM-5.2 时不能改写自定义策略';
    END IF;
END $$;
-- 已归到其他档位的 SenseNova 不能被复制到 GLM-5.2 所在档位。
UPDATE settings SET value = '{"enabled":true,"tiers":[
    {"name":"custom","models":["glm-5.2","qwen3.7-max"]},
    {"name":"other","models":["sensenova-6.8-flash-lite","minimax-m3"]}
]}' WHERE key = 'model_fallback_policy';
CREATE TEMP TABLE assigned_elsewhere ON COMMIT DROP AS SELECT * FROM settings;
\ir ../263_add_glm52_sensenova_fallback.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM assigned_elsewhere) THEN
        RAISE EXCEPTION '不能复制管理员已归到其他档位的 SenseNova';
    END IF;
END $$;


ROLLBACK;
\echo 'GLM-5.2 / SenseNova fallback migration checks passed'
