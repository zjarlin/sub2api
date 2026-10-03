-- DeepSeek V4.1 Flash 的档位降级优先 GLM/Qwen，并降低 GPT-5.6 Terra 的降级权重。
-- 保留其他档位、未识别模型及管理员自定义的档位顺序。
DO $$
DECLARE
    stored_policy JSONB;
    updated_policy JSONB;
    tier_index INTEGER;
    reordered_models JSONB;
BEGIN
    SELECT value::jsonb INTO stored_policy
    FROM settings WHERE key = 'model_fallback_policy'
    FOR UPDATE;

    IF stored_policy IS NULL OR stored_policy->'tiers' IS NULL THEN
        RETURN;
    END IF;

    SELECT (ordinality - 1)::integer INTO tier_index
    FROM jsonb_array_elements(stored_policy->'tiers') WITH ORDINALITY AS t(tier, ordinality)
    WHERE tier->'models' ? 'deepseek-v4.1-flash'
      AND tier->'models' ? 'gpt-5.6-terra'
    LIMIT 1;

    IF tier_index IS NULL THEN
        RETURN;
    END IF;

    SELECT jsonb_agg(model ORDER BY priority, ordinality)
    INTO reordered_models
    FROM (
        SELECT value AS model,
               ordinality,
               CASE
                   WHEN value = 'deepseek-v4.1-flash' THEN 0
                   WHEN lower(value) LIKE 'glm-%'
                     OR lower(value) LIKE '%/glm-%' THEN 1
                   WHEN lower(value) LIKE 'qwen%'
                     OR lower(value) LIKE '%/qwen%' THEN 2
                   WHEN value = 'gpt-5.6-terra' THEN 4
                   ELSE 3
               END AS priority
        FROM jsonb_array_elements_text(stored_policy->'tiers'->tier_index->'models') WITH ORDINALITY AS m(value, ordinality)
    ) ordered_models;

    updated_policy := jsonb_set(
        stored_policy,
        ARRAY['tiers', tier_index::text, 'models'],
        reordered_models,
        false
    );

    IF updated_policy IS DISTINCT FROM stored_policy THEN
        UPDATE settings SET value = updated_policy::text, updated_at = NOW()
        WHERE key = 'model_fallback_policy';
    END IF;
END $$;
