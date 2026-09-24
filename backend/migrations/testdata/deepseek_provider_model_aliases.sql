-- 用法：psql -X -v ON_ERROR_STOP=1 -f migrations/testdata/deepseek_provider_model_aliases.sql
-- 所有读写限于会话临时表，结束时回滚，不触碰实际 settings 或迁移历史。
\set ON_ERROR_STOP on
BEGIN;
CREATE TEMP TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
) ON COMMIT DROP;
\ir ../244_model_aliases_and_fallback_policy.sql
CREATE TEMP TABLE initial_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../246_deepseek_provider_model_aliases.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
BEGIN
    IF (SELECT count(*) FROM jsonb_array_elements(p->'groups') AS g
        WHERE g->'aliases' ? ('deepseek/' || (g->>'canonical'))) <> 3 THEN
        RAISE EXCEPTION '新环境必须将三个 DeepSeek 规范 ID 映射到上游带前缀的 ID';
    END IF;
    IF EXISTS (SELECT 1 FROM settings s JOIN initial_settings i USING (key)
        WHERE s.key <> 'model_aliases' AND s.value IS DISTINCT FROM i.value) THEN
        RAISE EXCEPTION '不能改变降级档位或其他功能配置';
    END IF;
END $$;
CREATE TEMP TABLE after_first_run ON COMMIT DROP AS SELECT * FROM settings;
\ir ../246_deepseek_provider_model_aliases.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持配置和更新时间不变';
    END IF;
END $$;

-- 升级时保留自定义别名、已删除的组，以及已经归属其他组的带前缀 ID。
UPDATE settings SET value = '{"custom":true,"groups":[
    {"canonical":"deepseek-v4-flash","aliases":["private-flash"]},
    {"canonical":"deepseek-v4.1-flash","aliases":["private-v41"]},
    {"canonical":"custom-route","aliases":["deepseek/deepseek-v4.1-flash"]}
]}' WHERE key = 'model_aliases';
\ir ../246_deepseek_provider_model_aliases.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
BEGIN
    IF p <> '{"custom":true,"groups":[
        {"canonical":"deepseek-v4-flash","aliases":["private-flash","deepseek/deepseek-v4-flash"]},
        {"canonical":"deepseek-v4.1-flash","aliases":["private-v41"]},
        {"canonical":"custom-route","aliases":["deepseek/deepseek-v4.1-flash"]}
    ]}'::jsonb THEN
        RAISE EXCEPTION '升级必须保留管理员的等价关系与自定义字段';
    END IF;
END $$;
UPDATE settings SET value = '{"groups":[]}' WHERE key = 'model_aliases';
\ir ../246_deepseek_provider_model_aliases.sql
DO $$
BEGIN
    IF (SELECT value FROM settings WHERE key = 'model_aliases') <> '{"groups":[]}' THEN
        RAISE EXCEPTION '不能重新启用已清空的别名配置';
    END IF;
END $$;
ROLLBACK;
\echo 'DeepSeek provider alias migration checks passed'
