-- 客户端可能把 V4.1 Flash 的版本号写成短横线；缺少该同义词时无法命中规范 ID。
-- 只追加别名，保留空策略、管理员删除的组、已有 ID 归属和其他设置。
DO $$
DECLARE
    stored_policy JSONB;
    updated_policy JSONB;
    group_index INTEGER;
BEGIN
    SELECT value::jsonb INTO stored_policy
    FROM settings WHERE key = 'model_aliases'
    FOR UPDATE;

    IF stored_policy IS NULL OR stored_policy->'groups' = '[]'::jsonb THEN
        RETURN;
    END IF;

    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(stored_policy->'groups') AS g
        WHERE g->>'canonical' = 'deepseek-v4-1-flash' OR g->'aliases' ? 'deepseek-v4-1-flash'
    ) THEN
        RETURN;
    END IF;

    SELECT (ordinality - 1)::integer INTO group_index
    FROM jsonb_array_elements(stored_policy->'groups') WITH ORDINALITY AS g(value, ordinality)
    WHERE value->>'canonical' = 'deepseek-v4.1-flash';

    -- 已删除或改名的规范组不在这里恢复，避免覆盖管理员配置。
    IF group_index IS NULL THEN
        RETURN;
    END IF;

    updated_policy := jsonb_set(
        stored_policy,
        ARRAY['groups', group_index::text, 'aliases'],
        COALESCE(stored_policy->'groups'->group_index->'aliases', '[]'::jsonb)
            || jsonb_build_array('deepseek-v4-1-flash')
    );

    IF updated_policy IS DISTINCT FROM stored_policy THEN
        UPDATE settings SET value = updated_policy::text, updated_at = NOW()
        WHERE key = 'model_aliases';
    END IF;
END $$;
