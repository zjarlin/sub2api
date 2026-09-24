-- 上游目录使用 deepseek/ 前缀；缺少同义词会使这些账号无法参与规范 ID 的调度及降级。
-- 只扩展已有的 DeepSeek 同义词组，保留档位顺序、其他设置及管理员主动删除的组。
-- 已归属其他组的 ID 不重新分配，避免覆盖管理员定义的等价关系。
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

    FOR addition IN
        SELECT * FROM (VALUES
            ('deepseek-v4-flash', 'deepseek/deepseek-v4-flash'),
            ('deepseek-v4-pro', 'deepseek/deepseek-v4-pro'),
            ('deepseek-v4.1-flash', 'deepseek/deepseek-v4.1-flash')
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
