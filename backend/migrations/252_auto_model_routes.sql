-- 路由观测与候选计划独立持久化，不写入模型对话内容。
CREATE TABLE IF NOT EXISTS auto_model_route_plans (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    session_id UUID NOT NULL,
    plan_id VARCHAR(64) NOT NULL,
    candidates JSONB NOT NULL CHECK (jsonb_typeof(candidates) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (api_key_id, session_id, plan_id)
);

CREATE TABLE IF NOT EXISTS auto_model_routes (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    session_id UUID NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    request_id UUID NOT NULL,
    selected_model VARCHAR(512) NOT NULL,
    resolved_model VARCHAR(512) NOT NULL DEFAULT '',
    attempted_models JSONB NOT NULL CHECK (jsonb_typeof(attempted_models) = 'array'),
    plan_id VARCHAR(64) NOT NULL DEFAULT '',
    state VARCHAR(16) NOT NULL CHECK (state IN ('selected', 'responding', 'completed', 'failed', 'interrupted')),
    started_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    revision BIGINT NOT NULL,
    PRIMARY KEY (api_key_id, session_id, request_id)
);

CREATE INDEX IF NOT EXISTS idx_auto_model_routes_session
    ON auto_model_routes (api_key_id, session_id, started_at DESC, request_id DESC);
CREATE INDEX IF NOT EXISTS idx_auto_model_routes_run
    ON auto_model_routes (api_key_id, session_id, run_id, started_at DESC, request_id DESC);
