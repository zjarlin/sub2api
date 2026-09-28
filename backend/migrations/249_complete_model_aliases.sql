-- 补齐已确认指向同一模型的名称，保留管理员定义的归属和空策略。
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

    IF stored_policy IS NULL THEN
        RETURN;
    END IF;
    updated_policy := stored_policy;

    IF jsonb_array_length(updated_policy->'groups') > 0 AND NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(updated_policy->'groups') AS g
        WHERE g->>'canonical' IN ('minimax-m2.7', 'cn:minimax-m2.7', 'MiniMax-M2.7')
           OR g->'aliases' ?| ARRAY['minimax-m2.7', 'cn:minimax-m2.7', 'MiniMax-M2.7']
    ) THEN
        updated_policy := jsonb_set(
            updated_policy,
            '{groups}',
            updated_policy->'groups' || jsonb_build_array(jsonb_build_object(
                'canonical', 'minimax-m2.7', 'aliases', jsonb_build_array(
                    'cn:minimax-m2.7', 'MiniMax-M2.7'
                )
            ))
        );
    END IF;

    FOR addition IN
        SELECT * FROM (VALUES
            ('deepseek-v4.1-flash', 'DeepSeek-V4.1-Flash'),
            ('deepseek-v4.1-flash', 'deepseek-flash'),
            ('minimax-m3', 'MiniMax-M3'),
            ('minimax-m2.7', 'cn:minimax-m2.7'),
            ('minimax-m2.7', 'MiniMax-M2.7')
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
