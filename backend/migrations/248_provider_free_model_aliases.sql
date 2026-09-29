-- 补齐上游白名单中的 Claude / GLM / Qwen 同义词；flash-next 与 flash 不合并。
-- 保留显式清空的策略和已有 ID 归属；只更新别名，不修改档位或账号映射。
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
            ('claude-opus-4-7', 'anthropic/claude-opus-4.7'),
            ('claude-sonnet-4-6', 'anthropic/claude-sonnet-4.6'),
            ('glm-5.2', 'free-glm-5.2'),
            ('glm-5.1', 'cn:glm-5.1'),
            ('qwen3.8-max', 'free-qwen-3.8-max'),
            ('qwen3.8-flash-next', 'free-qwen-3.8-flash-next')
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

        IF group_index IS NULL THEN
            -- 规范 ID 若已被管理员作为其他组的别名使用，不改变其归属。
            IF EXISTS (
                SELECT 1 FROM jsonb_array_elements(updated_policy->'groups') AS g
                WHERE g->'aliases' ? addition.canonical
            ) THEN
                CONTINUE;
            END IF;
            updated_policy := jsonb_set(updated_policy, '{groups}',
                (updated_policy->'groups') || jsonb_build_array(jsonb_build_object(
                    'canonical', addition.canonical,
                    'aliases', jsonb_build_array(addition.alias)
                )));
        ELSE
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
