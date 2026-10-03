-- 用法：psql -X -v ON_ERROR_STOP=1 -f migrations/testdata/deepseek_v4_family_prefer_v41.sql
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
\ir ../260_deepseek_v41_flash_hyphen_alias.sql
CREATE TEMP TABLE initial_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../261_deepseek_v4_family_prefer_v41.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
    target JSONB;
BEGIN
    SELECT value INTO target
    FROM jsonb_array_elements(p->'groups') AS g(value)
    WHERE value->>'canonical' = 'deepseek-v4.1-flash';

    IF target IS NULL THEN
        RAISE EXCEPTION 'V4.1 Flash 目标组缺失';
    END IF;

    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(p->'groups') AS g(value)
        WHERE g.value->>'canonical' IN ('deepseek-v4-flash', 'deepseek-v4-pro')
    ) THEN
        RAISE EXCEPTION 'V4 Flash / Pro 不应继续作为独立规范组';
    END IF;

    IF NOT (target->'aliases' ? 'deepseek-v4-flash')
        OR NOT (target->'aliases' ? 'deepseek/deepseek-v4-flash')
        OR NOT (target->'aliases' ? 'DeepSeek-V4-Flash')
        OR NOT (target->'aliases' ? 'deepseek-v4-pro')
        OR NOT (target->'aliases' ? 'deepseek/deepseek-v4-pro')
        OR NOT (target->'aliases' ? 'DeepSeek-V4-Pro')
        OR NOT (target->'aliases' ? 'opencode-go/deepseek-v4-flash')
        OR NOT (target->'aliases' ? 'opencode-go/deepseek-v4-pro') THEN
        RAISE EXCEPTION 'V4.1 Flash 未完整接管 V4 Flash / Pro 同义词';
    END IF;

    IF target->'aliases'->>0 NOT IN (
        'cn:deepseek-v4.1-flash', 'cline-pass/deepseek-v4.1-flash',
        'deepseek/deepseek-v4.1-flash', 'deepseek-4.1-flash',
        'DeepSeek-V4.1-Flash', 'deepseek-flash', 'deepseek-v4-1-flash'
    ) THEN
        RAISE EXCEPTION 'V4.1 Flash 同义词必须优先于兼容模型';
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
\ir ../261_deepseek_v4_family_prefer_v41.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持配置和更新时间不变';
    END IF;
END $$;

-- 其他规范组已经拥有的 ID 不能被搬回 DeepSeek 组。
UPDATE settings SET value = '{"groups":[
    {"canonical":"deepseek-v4-flash","aliases":["flash-private"]},
    {"canonical":"deepseek-v4-pro","aliases":["pro-private"]},
    {"canonical":"deepseek-v4.1-flash","aliases":["deepseek-flash"]},
    {"canonical":"custom-route","aliases":["deepseek/deepseek-v4-pro"]}
]}' WHERE key = 'model_aliases';
\ir ../261_deepseek_v4_family_prefer_v41.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
    target JSONB;
BEGIN
    SELECT value INTO target
    FROM jsonb_array_elements(p->'groups') AS g(value)
    WHERE value->>'canonical' = 'deepseek-v4.1-flash';

    IF NOT (target->'aliases' ? 'flash-private')
        OR NOT (target->'aliases' ? 'pro-private') THEN
        RAISE EXCEPTION '未接管 V4 Flash / Pro 的私有别名';
    END IF;

    IF target->'aliases' ? 'deepseek/deepseek-v4-pro' THEN
        RAISE EXCEPTION '不能抢占其他规范组的 ID';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(p->'groups') AS g(value)
        WHERE g.value->>'canonical' = 'custom-route'
          AND g.value->'aliases' ? 'deepseek/deepseek-v4-pro'
    ) THEN
        RAISE EXCEPTION '其他规范组归属必须保留';
    END IF;
END $$;

UPDATE settings SET value = '{"groups":[]}' WHERE key = 'model_aliases';
\ir ../261_deepseek_v4_family_prefer_v41.sql
DO $$
BEGIN
    IF (SELECT value FROM settings WHERE key = 'model_aliases') <> '{"groups":[]}' THEN
        RAISE EXCEPTION '空同义词策略必须保持为空';
    END IF;
END $$;

DELETE FROM settings WHERE key = 'model_aliases';
\ir ../261_deepseek_v4_family_prefer_v41.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM settings WHERE key = 'model_aliases') THEN
        RAISE EXCEPTION '不能重新创建已删除的策略';
    END IF;
END $$;
ROLLBACK;
\echo 'DeepSeek V4 family prefer V4.1 Flash migration checks passed'
