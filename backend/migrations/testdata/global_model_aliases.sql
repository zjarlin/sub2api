-- 用法：psql -X -U postgres -d postgres -v ON_ERROR_STOP=1 -f migrations/testdata/global_model_aliases.sql
-- 所有读写限于会话临时表，结束时回滚。
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
\ir ../254_global_model_aliases.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
    expected RECORD;
BEGIN
    FOR expected IN SELECT * FROM (VALUES
        ('deepseek-v4-flash', 'deepseek/deepseek-v4-flash'),
        ('deepseek-v4-pro', 'deepseek/deepseek-v4-pro'),
        ('deepseek-v4.1-flash', 'deepseek/deepseek-v4.1-flash'),
        ('deepseek-v4.1-flash', 'DeepSeek-V4.1-Flash'),
        ('deepseek-v4.1-flash', 'deepseek-flash'),
        ('glm-5.3', 'free-glm-5.3'),
        ('glm-5.2', 'free-glm-5.2'),
        ('minimax-m3', 'MiniMax-M3'),
        ('claude-opus-4-7', 'anthropic/claude-opus-4.7'),
        ('claude-sonnet-4-6', 'anthropic/claude-sonnet-4.6')
    ) AS aliases(canonical, alias) LOOP
        IF NOT EXISTS (SELECT 1 FROM jsonb_array_elements(p->'groups') g
            WHERE g->>'canonical' = expected.canonical AND g->'aliases' ? expected.alias) THEN
            RAISE EXCEPTION '缺少全局同义词：%', expected.alias;
        END IF;
    END LOOP;
    IF EXISTS (SELECT 1 FROM settings s JOIN initial_settings i USING (key)
        WHERE s.key <> 'model_aliases' AND s.value IS DISTINCT FROM i.value) THEN
        RAISE EXCEPTION '不能改变其他配置';
    END IF;
END $$;
CREATE TEMP TABLE after_first_run ON COMMIT DROP AS SELECT * FROM settings;
\ir ../254_global_model_aliases.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持配置和更新时间不变';
    END IF;
END $$;
UPDATE settings SET value = '{"groups":[]}' WHERE key = 'model_aliases';
\ir ../254_global_model_aliases.sql
DO $$
BEGIN
    IF (SELECT value FROM settings WHERE key = 'model_aliases') <> '{"groups":[]}' THEN
        RAISE EXCEPTION '空同义词策略必须保持为空';
    END IF;
END $$;
ROLLBACK;
\echo 'Global model alias migration checks passed'
