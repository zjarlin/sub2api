ALTER TABLE auto_model_routes ADD COLUMN IF NOT EXISTS operation JSONB;
ALTER TABLE auto_model_routes DROP CONSTRAINT IF EXISTS auto_model_routes_state_check;
ALTER TABLE auto_model_routes ADD CONSTRAINT auto_model_routes_state_check
    CHECK (state IN ('selected', 'responding', 'queued', 'running', 'completed', 'failed', 'interrupted'));
