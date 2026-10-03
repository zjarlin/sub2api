-- 用法：psql -X -v ON_ERROR_STOP=1 -f migrations/testdata/deepseek_v41_flash_hyphen_alias.sql
-- 仅操作会话临时表，结束时回滚。
\set ON_ERROR_STOP on
BEGIN;
CREATE TEMP TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
) ON COMMIT DROP;
\ir ../244_model_aliases_and_fallback_policy.sql
\ir ../246_deepseek_provider_model_aliases.sql
\ir ../248_provider_free_model_aliases.sql
\ir ../249_complete_model_aliases.sql
\ir ../254_global_model_aliases.sql
CREATE TEMP TABLE initial_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../260_deepseek_v41_flash_hyphen_alias.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(p->'groups') AS g
        WHERE g->>'canonical' = 'deepseek-v4.1-flash'
          AND g->'aliases' ? 'deepseek-v4-1-flash'
    ) THEN
        RAISE EXCEPTION 'V4.1 Flash 缺少短横线版本号同义词';
    END IF;

    IF EXISTS (
        SELECT id FROM (
            SELECT g->>'canonical' AS id FROM jsonb_array_elements(p->'groups') g
            UNION ALL
            SELECT a FROM jsonb_array_elements(p->'groups') g,
                jsonb_array_elements_text(g->'aliases') a
        ) ids GROUP BY id HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION '模型 ID 必须唯一归属';
    END IF;

    IF EXISTS (SELECT 1 FROM settings s JOIN initial_settings i USING (key)
        WHERE s.key <> 'model_aliases' AND s.value IS DISTINCT FROM i.value) THEN
        RAISE EXCEPTION '不能改变其他配置';
    END IF;
END $$;

CREATE TEMP TABLE after_first_run ON COMMIT DROP AS SELECT * FROM settings;
\ir ../260_deepseek_v41_flash_hyphen_alias.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持配置和更新时间不变';
    END IF;
END $$;

-- 已有归属优先：不能把管理员放在其他组的 ID 抢回标准组。
UPDATE settings SET value = '{"groups":[
    {"canonical":"deepseek-v4.1-flash","aliases":["deepseek-flash"]},
    {"canonical":"custom-route","aliases":["deepseek-v4-1-flash"]}
]}' WHERE key = 'model_aliases';
CREATE TEMP TABLE custom_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../260_deepseek_v41_flash_hyphen_alias.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM custom_settings) THEN
        RAISE EXCEPTION '必须保留管理员的 ID 归属';
    END IF;
END $$;

UPDATE settings SET value = '{"groups":[]}' WHERE key = 'model_aliases';
\ir ../260_deepseek_v41_flash_hyphen_alias.sql
DO $$
BEGIN
    IF (SELECT value FROM settings WHERE key = 'model_aliases') <> '{"groups":[]}' THEN
        RAISE EXCEPTION '空同义词策略必须保持为空';
    END IF;
END $$;

DELETE FROM settings WHERE key = 'model_aliases';
\ir ../260_deepseek_v41_flash_hyphen_alias.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM settings WHERE key = 'model_aliases') THEN
        RAISE EXCEPTION '不能重新创建已删除的策略';
    END IF;
END $$;
ROLLBACK;
\echo 'DeepSeek V4.1 Flash hyphen alias migration checks passed'
