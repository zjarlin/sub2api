-- 规范名使用 DeepSeek 最新 ID deepseek-flash；ds 是仅对外展示的树根。
-- 新日期版优先路由到已确认的 Cline 地址；保留其他管理员配置和账号白名单。
DO $$
DECLARE
    stored JSONB;
    updated JSONB;
    groups JSONB;
    target JSONB;
    target_index INTEGER;
    ids JSONB;
BEGIN
    SELECT value::jsonb INTO stored FROM settings WHERE key = 'model_aliases' FOR UPDATE;
    IF stored IS NULL OR stored->'groups' IS NULL OR stored->'groups' = '[]'::jsonb THEN RETURN; END IF;
    groups := stored->'groups';
    SELECT value, (ordinality - 1)::integer INTO target, target_index
    FROM jsonb_array_elements(groups) WITH ORDINALITY AS g(value, ordinality)
    WHERE value->>'canonical' = 'deepseek-v4.1-flash';
    IF target IS NULL THEN RETURN; END IF;
    -- 自定义树或路由策略由管理员维护，迁移只升级原来的平铺组。
    IF COALESCE(target->>'parent','') <> '' OR COALESCE(target->'routes','[]'::jsonb) <> '[]'::jsonb
       OR target->'aliases' ? 'ds'
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(groups) AS g WHERE g->>'parent' = 'deepseek-v4.1-flash') THEN RETURN; END IF;
    -- 管理员已将新名称分配到别处时不抢占归属。
    IF EXISTS (SELECT 1 FROM jsonb_array_elements(groups) AS g
        WHERE g->>'canonical' <> 'deepseek-v4.1-flash'
          AND (g->>'canonical' IN ('ds','deepseek-flash','deepseek/deepseek-v4.1-flash-1017') OR g->'aliases' ?| ARRAY['ds','deepseek-flash','deepseek/deepseek-v4.1-flash-1017'])) THEN RETURN; END IF;
    SELECT jsonb_agg(id ORDER BY first_position) INTO ids FROM (
        SELECT id, min(position) AS first_position
        FROM (
            SELECT 'deepseek-v4.1-flash'::text AS id, 0::bigint AS position
            UNION ALL
            SELECT value, ordinality FROM jsonb_array_elements_text(COALESCE(target->'aliases','[]')) WITH ORDINALITY
            WHERE value NOT IN ('deepseek-flash','deepseek/deepseek-v4.1-flash-1017')
            UNION ALL SELECT 'deepseek/deepseek-v4.1-flash-1017', 2049
        ) source GROUP BY id
    ) unique_ids;
    target := target || jsonb_build_object(
        'canonical','deepseek-flash','parent','ds','aliases',ids,
        'routes',jsonb_build_array(
            jsonb_build_object('model','deepseek/deepseek-v4.1-flash-1017','priority',0,'base_urls',jsonb_build_array('https://api.cline.bot/api')),
            jsonb_build_object('model','deepseek-flash','priority',1),
            jsonb_build_object('model','deepseek/deepseek-v4.1-flash','priority',2),
            jsonb_build_object('model','deepseek-v4.1-flash','priority',3),
            jsonb_build_object('model','cline-pass/deepseek-v4.1-flash','priority',4,'base_urls',jsonb_build_array('https://api.cline.bot/api'))
        )
    );
    -- 已存在的策略缺少某个精确 ID 时只在未被别组占用的情况下追加。
    IF NOT target->'aliases' ? 'deepseek/deepseek-v4.1-flash' THEN
        IF EXISTS (SELECT 1 FROM jsonb_array_elements(groups) AS g
          WHERE g->>'canonical' <> 'deepseek-v4.1-flash'
          AND (g->>'canonical' = 'deepseek/deepseek-v4.1-flash' OR g->'aliases' ? 'deepseek/deepseek-v4.1-flash')) THEN RETURN; END IF;
        target := jsonb_set(target,'{aliases}',target->'aliases' || '"deepseek/deepseek-v4.1-flash"'::jsonb);
    END IF;
    IF NOT target->'aliases' ? 'cline-pass/deepseek-v4.1-flash' THEN
        -- 已删除的渠道路由不自动恢复。
        target := jsonb_set(target,'{routes}',(target->'routes') - 4);
    END IF;
    updated := jsonb_set(stored,ARRAY['groups',target_index::text],target);
    updated := jsonb_set(updated,'{groups}',jsonb_build_array(jsonb_build_object('canonical','ds','aliases','[]'::jsonb)) || (updated->'groups'));
    IF updated IS DISTINCT FROM stored THEN
        UPDATE settings SET value=updated::text,updated_at=NOW() WHERE key='model_aliases';
    END IF;
END $$;
