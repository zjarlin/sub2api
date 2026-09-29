-- 用法：psql -X -v ON_ERROR_STOP=1 -f migrations/testdata/provider_free_model_aliases.sql
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
CREATE TEMP TABLE initial_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../248_provider_free_model_aliases.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
    expected RECORD;
BEGIN
    FOR expected IN SELECT * FROM (VALUES
        ('claude-opus-4-7', 'anthropic/claude-opus-4.7'),
        ('claude-sonnet-4-6', 'anthropic/claude-sonnet-4.6'),
        ('glm-5.2', 'free-glm-5.2'),
        ('glm-5.1', 'cn:glm-5.1'),
        ('qwen3.8-max', 'free-qwen-3.8-max'),
        ('qwen3.8-flash-next', 'free-qwen-3.8-flash-next')
    ) AS aliases(canonical, alias) LOOP
        IF NOT EXISTS (SELECT 1 FROM jsonb_array_elements(p->'groups') g
            WHERE g->>'canonical' = expected.canonical AND g->'aliases' ? expected.alias) THEN
            RAISE EXCEPTION '缺少同义词：%', expected.alias;
        END IF;
    END LOOP;
    IF EXISTS (SELECT 1 FROM settings s JOIN initial_settings i USING (key)
        WHERE s.key <> 'model_aliases' AND s.value IS DISTINCT FROM i.value) THEN
        RAISE EXCEPTION '不能改变其他配置';
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
END $$;
CREATE TEMP TABLE after_first_run ON COMMIT DROP AS SELECT * FROM settings;
\ir ../248_provider_free_model_aliases.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持配置和更新时间不变';
    END IF;
END $$;

-- 所有新增 ID 都发生冲突：保留原归属，包括规范 ID 被用作别名的情况。
UPDATE settings SET value = '{"custom":true,"groups":[
    {"canonical":"custom-route","aliases":["claude-opus-4-7","free-glm-5.2","cn:glm-5.1","free-qwen-3.8-max","qwen3.8-flash-next"]},
    {"canonical":"anthropic/claude-sonnet-4.6","aliases":[]},
    {"canonical":"glm-5.2","aliases":["private-glm"]}
]}' WHERE key = 'model_aliases';
CREATE TEMP TABLE custom_settings ON COMMIT DROP AS SELECT * FROM settings;
\ir ../248_provider_free_model_aliases.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM custom_settings) THEN
        RAISE EXCEPTION '必须保留管理员的 ID 归属与自定义字段';
    END IF;
END $$;
UPDATE settings SET value = '{"groups":[]}' WHERE key = 'model_aliases';
\ir ../248_provider_free_model_aliases.sql
DO $$
BEGIN
    IF (SELECT value FROM settings WHERE key = 'model_aliases') <> '{"groups":[]}' THEN
        RAISE EXCEPTION '不能重新启用已清空的策略';
    END IF;
END $$;
DELETE FROM settings WHERE key = 'model_aliases';
\ir ../248_provider_free_model_aliases.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM settings WHERE key = 'model_aliases') THEN
        RAISE EXCEPTION '不能重新创建已删除的策略';
    END IF;
END $$;
ROLLBACK;
\echo 'Provider/free model alias migration checks passed'
