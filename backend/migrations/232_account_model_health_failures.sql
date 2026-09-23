ALTER TABLE account_model_health
    ADD COLUMN IF NOT EXISTS last_failure_at TIMESTAMPTZ;

UPDATE account_model_health h
SET last_failure_at = failures.failed_at
FROM (
    SELECT account_id,
           COALESCE(NULLIF(BTRIM(requested_model), ''), BTRIM(model)) AS model,
           MAX(created_at) AS failed_at
    FROM ops_error_logs
    WHERE account_id IS NOT NULL
      AND COALESCE(NULLIF(BTRIM(requested_model), ''), BTRIM(model)) <> ''
    GROUP BY account_id, COALESCE(NULLIF(BTRIM(requested_model), ''), BTRIM(model))
) failures
WHERE h.account_id = failures.account_id
  AND h.model = failures.model
  AND (h.last_failure_at IS NULL OR failures.failed_at > h.last_failure_at);

CREATE OR REPLACE FUNCTION record_usage_account_model_health()
RETURNS TRIGGER AS $$
DECLARE
    health_model TEXT;
BEGIN
    health_model := COALESCE(NULLIF(BTRIM(NEW.requested_model), ''), BTRIM(NEW.model));
    IF health_model = '' THEN
        RETURN NEW;
    END IF;
    IF NEW.actual_cost <= 0 THEN
        UPDATE account_model_health
        SET last_failure_at = GREATEST(COALESCE(last_failure_at, NEW.created_at), NEW.created_at)
        WHERE account_id = NEW.account_id AND model = health_model;
        RETURN NEW;
    END IF;
    INSERT INTO account_model_health (account_id, model, last_success_at, source)
    VALUES (NEW.account_id, health_model, NEW.created_at, 'usage')
    ON CONFLICT (account_id, model) DO UPDATE
    SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
        source = CASE
            WHEN EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
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
        UPDATE account_model_health
        SET last_failure_at = GREATEST(COALESCE(last_failure_at, NEW.finished_at), NEW.finished_at)
        WHERE account_id = health_account_id AND model = health_model;
        RETURN NEW;
    END IF;
    INSERT INTO account_model_health (account_id, model, last_success_at, source)
    VALUES (health_account_id, health_model, NEW.finished_at, 'scheduled_test')
    ON CONFLICT (account_id, model) DO UPDATE
    SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
        source = CASE
            WHEN EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
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
    UPDATE account_model_health
    SET last_failure_at = GREATEST(COALESCE(last_failure_at, NEW.created_at), NEW.created_at)
    WHERE account_id = NEW.account_id AND model = health_model;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_ops_error_account_model_health ON ops_error_logs;
CREATE TRIGGER trg_ops_error_account_model_health
AFTER INSERT ON ops_error_logs
FOR EACH ROW
EXECUTE FUNCTION record_ops_error_account_model_health();
