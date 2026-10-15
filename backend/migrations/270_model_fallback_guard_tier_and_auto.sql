-- 为内容安全/审核类小模型补齐档位，并开启降级链末尾的 auto 兜底。
--
-- 背景：meta/llama-guard-4-12b 未被任何档位收录，按“未评级模型不猜测档位”的
-- 既有语义，同名账号重试耗尽后候选为空，请求直接以原上游错误返回，不会降级。
--
-- 本迁移做两件事，均只在管理员策略确实存在时生效，且保持幂等：
--   1) 追加「内容安全轻量」档，收纳 guard 类模型，使它们在同类内互降；
--   2) 打开 auto_fallback，使档位候选耗尽（含未评级模型）后由 Auto 的完整候选
--      与性价比顺序承接。guard/safety 模型本身被 Auto 文本判定排除，不会成为
--      自动候选，因此文本请求最终落到可用的通用文本模型。
DO $$
DECLARE
    stored_policy JSONB;
    updated_policy JSONB;
    guard_tier JSONB := jsonb_build_object(
        'name', '内容安全轻量',
        'models', jsonb_build_array(
            'meta/llama-guard-4-12b',
            'nvidia/llama-3.1-nemoguard-8b-content-safety',
            'nvidia/llama-3.1-nemoguard-8b-topic-control',
            'nvidia/llama-3.1-nemotron-safety-guard-8b-v3'
        )
    );
BEGIN
    SELECT value::jsonb INTO stored_policy
    FROM settings WHERE key = 'model_fallback_policy'
    FOR UPDATE;

    -- 未配置策略时保留内置推荐（代码里的 DefaultModelFallbackPolicy 已含本档）。
    IF stored_policy IS NULL OR stored_policy->'tiers' IS NULL THEN
        RETURN;
    END IF;

    updated_policy := jsonb_set(stored_policy, '{auto_fallback}', 'true'::jsonb, true);

    -- 已存在同名档位时只更新 auto_fallback，不重复追加档位。
    IF NOT EXISTS (
        SELECT 1
        FROM jsonb_array_elements(updated_policy->'tiers') AS t(tier)
        WHERE tier->>'name' = '内容安全轻量'
    ) THEN
        updated_policy := jsonb_set(
            updated_policy,
            '{tiers}',
            (updated_policy->'tiers') || jsonb_build_array(guard_tier),
            true
        );
    END IF;

    IF updated_policy IS DISTINCT FROM stored_policy THEN
        UPDATE settings SET value = updated_policy::text, updated_at = NOW()
        WHERE key = 'model_fallback_policy';
    END IF;
END $$;
