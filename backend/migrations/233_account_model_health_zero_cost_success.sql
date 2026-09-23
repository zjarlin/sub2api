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
            WHEN EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
            ELSE account_model_health.source
        END;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

INSERT INTO account_model_health (account_id, model, last_success_at, source)
SELECT account_id,
       COALESCE(NULLIF(BTRIM(requested_model), ''), BTRIM(model)) AS model,
       MAX(created_at) AS last_success_at,
       'usage' AS source
FROM usage_logs
WHERE account_id IS NOT NULL
  AND COALESCE(NULLIF(BTRIM(requested_model), ''), BTRIM(model)) <> ''
GROUP BY account_id, COALESCE(NULLIF(BTRIM(requested_model), ''), BTRIM(model))
ON CONFLICT (account_id, model) DO UPDATE
SET last_success_at = GREATEST(account_model_health.last_success_at, EXCLUDED.last_success_at),
    source = CASE
        WHEN EXCLUDED.last_success_at >= account_model_health.last_success_at THEN EXCLUDED.source
        ELSE account_model_health.source
    END;
