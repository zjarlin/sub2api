ALTER TABLE account_model_health
    ALTER COLUMN last_success_at DROP NOT NULL;

CREATE OR REPLACE FUNCTION record_usage_account_model_health()
RETURNS TRIGGER AS $$
DECLARE
    health_model TEXT;
BEGIN
    health_model := COALESCE(NULLIF(BTRIM(NEW.requested_model), ''), BTRIM(NEW.model));
    IF health_model = '' THEN
        RETURN NEW;
    END IF;
    INSERT INTO account_model_health (account_id, model, last_success_at, source)
    VALUES (NEW.account_id, health_model, NEW.created_at, 'usage')
    ON CONFLICT (account_id, model) DO UPDATE
    SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
        source = CASE
            WHEN account_model_health.last_success_at IS NULL OR EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
            ELSE account_model_health.source
        END;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION record_scheduled_test_account_model_health()
RETURNS TRIGGER AS $$
DECLARE
    health_account_id BIGINT;
    health_model TEXT;
BEGIN
    SELECT p.account_id, BTRIM(p.model_id)
    INTO health_account_id, health_model
    FROM scheduled_test_plans p
    WHERE p.id = NEW.plan_id;
    IF health_account_id IS NULL OR health_model = '' THEN
        RETURN NEW;
    END IF;
    IF NEW.status <> 'success' THEN
        INSERT INTO account_model_health (account_id, model, last_success_at, last_failure_at, source)
        VALUES (health_account_id, health_model, NULL, NEW.finished_at, 'scheduled_test')
        ON CONFLICT (account_id, model) DO UPDATE
        SET last_failure_at = GREATEST(
            COALESCE(account_model_health.last_failure_at, EXCLUDED.last_failure_at),
            EXCLUDED.last_failure_at
        );
        RETURN NEW;
    END IF;
    INSERT INTO account_model_health (account_id, model, last_success_at, source)
    VALUES (health_account_id, health_model, NEW.finished_at, 'scheduled_test')
    ON CONFLICT (account_id, model) DO UPDATE
    SET last_success_at = GREATEST(COALESCE(account_model_health.last_success_at, EXCLUDED.last_success_at), EXCLUDED.last_success_at),
        source = CASE
            WHEN account_model_health.last_success_at IS NULL OR EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
            ELSE account_model_health.source
        END;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION record_ops_error_account_model_health()
RETURNS TRIGGER AS $$
DECLARE
    health_model TEXT;
BEGIN
    IF NEW.account_id IS NULL THEN
        RETURN NEW;
    END IF;
    health_model := COALESCE(NULLIF(BTRIM(NEW.requested_model), ''), BTRIM(NEW.model));
    IF health_model = '' THEN
        RETURN NEW;
    END IF;
    INSERT INTO account_model_health (account_id, model, last_success_at, last_failure_at, source)
    VALUES (NEW.account_id, health_model, NULL, NEW.created_at, 'ops_error')
    ON CONFLICT (account_id, model) DO UPDATE
    SET last_failure_at = GREATEST(
        COALESCE(account_model_health.last_failure_at, EXCLUDED.last_failure_at),
        EXCLUDED.last_failure_at
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

INSERT INTO account_model_health (account_id, model, last_success_at, last_failure_at, source)
SELECT o.account_id,
       COALESCE(NULLIF(BTRIM(o.requested_model), ''), BTRIM(o.model)) AS model,
       NULL,
       MAX(o.created_at),
       'ops_error'
FROM ops_error_logs o
-- ops_error_logs 可能保留已删除账号的历史行，
-- account_model_health.account_id 有外键，回填时必须只取仍然存在的账号。
WHERE o.account_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM accounts a WHERE a.id = o.account_id)
  AND COALESCE(NULLIF(BTRIM(o.requested_model), ''), BTRIM(o.model)) <> ''
GROUP BY o.account_id, COALESCE(NULLIF(BTRIM(o.requested_model), ''), BTRIM(o.model))
ON CONFLICT (account_id, model) DO UPDATE
SET last_failure_at = GREATEST(
    COALESCE(account_model_health.last_failure_at, EXCLUDED.last_failure_at),
    EXCLUDED.last_failure_at
);
