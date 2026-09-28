-- 用法：psql -X -U postgres -d postgres -v ON_ERROR_STOP=1 -f migrations/testdata/complete_model_aliases.sql
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
\ir ../249_complete_model_aliases.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
BEGIN
    IF NOT EXISTS (SELECT 1 FROM jsonb_array_elements(p->'groups') AS g
        WHERE g->>'canonical' = 'deepseek-v4.1-flash'
          AND g->'aliases' ?& ARRAY['DeepSeek-V4.1-Flash', 'deepseek-flash']) THEN
        RAISE EXCEPTION 'V4.1 Flash 缺少新名或大小写同义词';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM jsonb_array_elements(p->'groups') AS g
        WHERE g->>'canonical' = 'minimax-m3' AND g->'aliases' ? 'MiniMax-M3') THEN
        RAISE EXCEPTION 'MiniMax M3 缺少官方大小写同义词';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM jsonb_array_elements(p->'groups') AS g
        WHERE g->>'canonical' = 'minimax-m2.7'
          AND g->'aliases' ?& ARRAY['cn:minimax-m2.7', 'MiniMax-M2.7']) THEN
        RAISE EXCEPTION 'MiniMax M2.7 缺少默认同义词组';
    END IF;
    IF EXISTS (SELECT 1 FROM settings s JOIN initial_settings i USING (key)
        WHERE s.key <> 'model_aliases' AND s.value IS DISTINCT FROM i.value) THEN
        RAISE EXCEPTION '迁移不能改变其他设置';
    END IF;
END $$;
CREATE TEMP TABLE after_first_run ON COMMIT DROP AS SELECT * FROM settings;
\ir ../249_complete_model_aliases.sql
DO $$
BEGIN
    IF EXISTS (SELECT * FROM settings EXCEPT SELECT * FROM after_first_run) THEN
        RAISE EXCEPTION '重复执行必须保持设置不变';
    END IF;
END $$;

UPDATE settings SET value = '{"groups":[
    {"canonical":"deepseek-v4.1-flash","aliases":["private-flash"]},
    {"canonical":"minimax-m3","aliases":["private-m3"]},
    {"canonical":"custom","aliases":["deepseek-flash","MiniMax-M2.7"]}
]}' WHERE key = 'model_aliases';
\ir ../249_complete_model_aliases.sql
DO $$
DECLARE
    p JSONB := (SELECT value::jsonb FROM settings WHERE key = 'model_aliases');
BEGIN
    IF p <> '{"groups":[
        {"canonical":"deepseek-v4.1-flash","aliases":["private-flash","DeepSeek-V4.1-Flash"]},
        {"canonical":"minimax-m3","aliases":["private-m3","MiniMax-M3"]},
        {"canonical":"custom","aliases":["deepseek-flash","MiniMax-M2.7"]}
    ]}'::jsonb THEN
        RAISE EXCEPTION '迁移不能覆盖管理员定义的别名归属';
    END IF;
END $$;
UPDATE settings SET value = '{"groups":[]}' WHERE key = 'model_aliases';
\ir ../249_complete_model_aliases.sql
DO $$
BEGIN
    IF (SELECT value FROM settings WHERE key = 'model_aliases') <> '{"groups":[]}' THEN
        RAISE EXCEPTION '空同义词策略必须保持为空';
    END IF;
END $$;
ROLLBACK;
\echo 'Model alias migration checks passed'
