-- V4 系列统一优先走 V4.1 Flash；账号不支持时按同组的 V4 Flash、V4 Pro 兼容 ID 降级。
-- 保留管理员对其他 ID 的归属、目标组扩展字段、组顺序及空/已删除策略。
DO $$
DECLARE
    stored_policy JSONB;
    updated_policy JSONB;
    updated_groups JSONB := '[]'::jsonb;
    target_group JSONB;
    source_group JSONB;
    source_ids JSONB := '[]'::jsonb;
    target_aliases JSONB;
    reserved_ids JSONB := '[]'::jsonb;
    group_value JSONB;
    source_canonical TEXT;
    model_id TEXT;
    source_found BOOLEAN := FALSE;
BEGIN
    SELECT value::jsonb INTO stored_policy
    FROM settings WHERE key = 'model_aliases'
    FOR UPDATE;

    IF stored_policy IS NULL OR stored_policy->'groups' = '[]'::jsonb THEN
        RETURN;
    END IF;

    SELECT value INTO target_group
    FROM jsonb_array_elements(stored_policy->'groups') AS g(value)
    WHERE value->>'canonical' = 'deepseek-v4.1-flash'
    LIMIT 1;

    -- 已删除或改名的目标组不在这里恢复，避免覆盖管理员配置。
    IF target_group IS NULL THEN
        RETURN;
    END IF;

    -- 兼容 ID 顺序固定：V4 Flash 整体先于 V4 Pro。
    FOREACH source_canonical IN ARRAY ARRAY['deepseek-v4-flash', 'deepseek-v4-pro']
    LOOP
        SELECT value INTO source_group
        FROM jsonb_array_elements(stored_policy->'groups') AS g(value)
        WHERE value->>'canonical' = source_canonical
        LIMIT 1;

        IF source_group IS NOT NULL THEN
            source_found := TRUE;
            source_ids := source_ids
                || jsonb_build_array(source_group->>'canonical')
                || COALESCE(source_group->'aliases', '[]'::jsonb);
            IF source_canonical = 'deepseek-v4-flash' THEN
                source_ids := source_ids || jsonb_build_array('opencode-go/deepseek-v4-flash');
            ELSIF source_canonical = 'deepseek-v4-pro' THEN
                source_ids := source_ids || jsonb_build_array('opencode-go/deepseek-v4-pro');
            END IF;
        END IF;
    END LOOP;

    IF NOT source_found THEN
        RETURN;
    END IF;

    WITH owners AS (
        SELECT g.value->>'canonical' AS owned_model_id
        FROM jsonb_array_elements(stored_policy->'groups') AS g(value)
        WHERE g.value->>'canonical' NOT IN (
            'deepseek-v4.1-flash', 'deepseek-v4-flash', 'deepseek-v4-pro'
        )
        UNION ALL
        SELECT alias.value
        FROM jsonb_array_elements(stored_policy->'groups') AS g(value)
        CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(g.value->'aliases', '[]'::jsonb)) AS alias(value)
        WHERE g.value->>'canonical' NOT IN (
            'deepseek-v4.1-flash', 'deepseek-v4-flash', 'deepseek-v4-pro'
        )
    )
    SELECT COALESCE(jsonb_agg(DISTINCT owned_model_id), '[]'::jsonb)
    INTO reserved_ids
    FROM owners;

    target_aliases := COALESCE(target_group->'aliases', '[]'::jsonb);
    FOR model_id IN SELECT jsonb_array_elements_text(source_ids)
    LOOP
        IF model_id = target_group->>'canonical'
            OR reserved_ids ? model_id
            OR target_aliases ? model_id THEN
            CONTINUE;
        END IF;
        target_aliases := target_aliases || jsonb_build_array(model_id);
    END LOOP;
    target_group := target_group || jsonb_build_object('aliases', target_aliases);

    FOR group_value IN SELECT value FROM jsonb_array_elements(stored_policy->'groups')
    LOOP
        IF group_value->>'canonical' = 'deepseek-v4.1-flash' THEN
            updated_groups := updated_groups || jsonb_build_array(target_group);
        ELSIF group_value->>'canonical' IN ('deepseek-v4-flash', 'deepseek-v4-pro') THEN
            CONTINUE;
        ELSE
            updated_groups := updated_groups || jsonb_build_array(group_value);
        END IF;
    END LOOP;

    updated_policy := jsonb_set(stored_policy, '{groups}', updated_groups);
    IF updated_policy IS DISTINCT FROM stored_policy THEN
        UPDATE settings SET value = updated_policy::text, updated_at = NOW()
        WHERE key = 'model_aliases';
    END IF;
END $$;
