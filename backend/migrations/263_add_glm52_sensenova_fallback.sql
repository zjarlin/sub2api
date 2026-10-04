-- 将 GLM-5.2 与 SenseNova 6.8 Flash Lite 纳入同一条降级路线。
-- 仅在已有 GLM-5.2 的档位中补齐，保留其他档位与管理员自定义策略。
DO $$
DECLARE
    stored_policy JSONB;
    updated_policy JSONB;
    tier_index INTEGER;
    model_id TEXT;
    updated_models JSONB := '[]'::jsonb;
BEGIN
    SELECT value::jsonb INTO stored_policy
    FROM settings WHERE key = 'model_fallback_policy'
    FOR UPDATE;

    IF stored_policy IS NULL OR stored_policy->'tiers' IS NULL THEN
        RETURN;
    END IF;

    SELECT (ordinality - 1)::integer INTO tier_index
    FROM jsonb_array_elements(stored_policy->'tiers') WITH ORDINALITY AS t(tier, ordinality)
    WHERE tier->'models' ? 'glm-5.2'
    LIMIT 1;

    -- 已删除或改名的策略不在这里恢复，避免覆盖管理员配置。
    IF tier_index IS NULL THEN
        RETURN;
    END IF;

    -- 管理员已把该模型放入其他档位时保留原归属，避免同一模型跨档重复。
    IF EXISTS (
        SELECT 1
        FROM jsonb_array_elements(stored_policy->'tiers') WITH ORDINALITY AS t(tier, ordinality)
        WHERE (ordinality - 1)::integer <> tier_index
          AND tier->'models' ? 'sensenova-6.8-flash-lite'
    ) THEN
        RETURN;
    END IF;

    FOR model_id IN
        SELECT value
        FROM jsonb_array_elements_text(stored_policy->'tiers'->tier_index->'models')
    LOOP
        -- 先移除旧位置，再固定放在 GLM-5.2 后面，使重复执行保持幂等。
        IF model_id = 'sensenova-6.8-flash-lite' THEN
            CONTINUE;
        END IF;
        updated_models := updated_models || jsonb_build_array(model_id);
        IF model_id = 'glm-5.2' THEN
            updated_models := updated_models || jsonb_build_array('sensenova-6.8-flash-lite');
        END IF;
    END LOOP;

    updated_policy := jsonb_set(
        stored_policy,
        ARRAY['tiers', tier_index::text, 'models'],
        updated_models,
        false
    );

    IF updated_policy IS DISTINCT FROM stored_policy THEN
        UPDATE settings SET value = updated_policy::text, updated_at = NOW()
        WHERE key = 'model_fallback_policy';
    END IF;
END $$;
