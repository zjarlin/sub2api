CREATE TABLE IF NOT EXISTS account_model_health (
    account_id      BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model           VARCHAR(512) NOT NULL,
    last_success_at TIMESTAMPTZ NOT NULL,
    source          VARCHAR(32) NOT NULL,
    PRIMARY KEY (account_id, model)
);

CREATE INDEX IF NOT EXISTS idx_account_model_health_last_success
    ON account_model_health (last_success_at DESC);

INSERT INTO account_model_health (account_id, model, last_success_at, source)
SELECT ul.account_id,
       COALESCE(NULLIF(BTRIM(ul.requested_model), ''), BTRIM(ul.model)),
       MAX(ul.created_at),
       'usage'
FROM usage_logs ul
WHERE ul.actual_cost > 0
  AND COALESCE(NULLIF(BTRIM(ul.requested_model), ''), BTRIM(ul.model)) <> ''
GROUP BY ul.account_id, COALESCE(NULLIF(BTRIM(ul.requested_model), ''), BTRIM(ul.model))
ON CONFLICT (account_id, model) DO UPDATE
SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
    source = CASE
        WHEN EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
        ELSE account_model_health.source
    END;

INSERT INTO account_model_health (account_id, model, last_success_at, source)
SELECT p.account_id, BTRIM(p.model_id), MAX(r.finished_at), 'scheduled_test'
FROM scheduled_test_plans p
JOIN scheduled_test_results r ON r.plan_id = p.id AND r.status = 'success'
WHERE BTRIM(p.model_id) <> ''
GROUP BY p.account_id, BTRIM(p.model_id)
ON CONFLICT (account_id, model) DO UPDATE
SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
    source = CASE
        WHEN EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
        ELSE account_model_health.source
    END;

CREATE OR REPLACE FUNCTION record_usage_account_model_health()
RETURNS TRIGGER AS $$
DECLARE
    health_model TEXT;
BEGIN
    IF NEW.actual_cost <= 0 THEN
        RETURN NEW;
    END IF;
    health_model := COALESCE(NULLIF(BTRIM(NEW.requested_model), ''), BTRIM(NEW.model));
    IF health_model = '' THEN
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

DROP TRIGGER IF EXISTS trg_usage_account_model_health ON usage_logs;
CREATE TRIGGER trg_usage_account_model_health
AFTER INSERT ON usage_logs
FOR EACH ROW
EXECUTE FUNCTION record_usage_account_model_health();

CREATE OR REPLACE FUNCTION record_scheduled_test_account_model_health()
RETURNS TRIGGER AS $$
DECLARE
    health_account_id BIGINT;
    health_model TEXT;
BEGIN
    IF NEW.status <> 'success' THEN
        RETURN NEW;
    END IF;
    SELECT p.account_id, BTRIM(p.model_id)
    INTO health_account_id, health_model
    FROM scheduled_test_plans p
    WHERE p.id = NEW.plan_id;
    IF health_account_id IS NULL OR health_model = '' THEN
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

DROP TRIGGER IF EXISTS trg_scheduled_test_account_model_health ON scheduled_test_results;
CREATE TRIGGER trg_scheduled_test_account_model_health
AFTER INSERT ON scheduled_test_results
FOR EACH ROW
EXECUTE FUNCTION record_scheduled_test_account_model_health();
