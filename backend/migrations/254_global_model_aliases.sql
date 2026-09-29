-- 补齐全局模型同义词，统一 provider 前缀与官方命名。
-- 只扩展现有规范 ID；保留管理员定义的归属、自定义字段和空策略。
DO $$
DECLARE
    stored_policy JSONB;
    updated_policy JSONB;
    addition RECORD;
    group_index INTEGER;
BEGIN
    SELECT value::jsonb INTO stored_policy
    FROM settings WHERE key = 'model_aliases'
    FOR UPDATE;

    IF stored_policy IS NULL OR stored_policy->'groups' = '[]'::jsonb THEN
        RETURN;
    END IF;
    updated_policy := stored_policy;

    FOR addition IN
        SELECT * FROM (VALUES
            ('deepseek-v4-flash', 'deepseek/deepseek-v4-flash'),
            ('deepseek-v4-pro', 'deepseek/deepseek-v4-pro'),
            ('deepseek-v4.1-flash', 'deepseek/deepseek-v4.1-flash'),
            ('deepseek-v4.1-flash', 'cline-pass/deepseek-v4.1-flash'),
            ('deepseek-v4.1-flash', 'DeepSeek-V4.1-Flash'),
            ('deepseek-v4.1-flash', 'deepseek-flash'),
            ('glm-5.3', 'free-glm-5.3'),
            ('glm-5.2', 'free-glm-5.2'),
            ('glm-5.1', 'free-glm-5.1'),
            ('minimax-m3', 'MiniMax-M3'),
            ('minimax-m2.7', 'MiniMax-M2.7'),
            ('claude-opus-4-7', 'anthropic/claude-opus-4.7'),
            ('claude-sonnet-4-6', 'anthropic/claude-sonnet-4.6')
        ) AS aliases(canonical, alias)
    LOOP
        IF EXISTS (
            SELECT 1 FROM jsonb_array_elements(updated_policy->'groups') AS g
            WHERE g->>'canonical' = addition.alias OR g->'aliases' ? addition.alias
        ) THEN
            CONTINUE;
        END IF;

        SELECT (ordinality - 1)::integer INTO group_index
        FROM jsonb_array_elements(updated_policy->'groups') WITH ORDINALITY AS g(value, ordinality)
        WHERE value->>'canonical' = addition.canonical;

        IF group_index IS NOT NULL THEN
            updated_policy := jsonb_set(
                updated_policy,
                ARRAY['groups', group_index::text, 'aliases'],
                COALESCE(updated_policy->'groups'->group_index->'aliases', '[]'::jsonb)
                    || jsonb_build_array(addition.alias)
            );
        END IF;
    END LOOP;

    IF updated_policy IS DISTINCT FROM stored_policy THEN
        UPDATE settings SET value = updated_policy::text, updated_at = NOW()
        WHERE key = 'model_aliases';
    END IF;
END $$;
