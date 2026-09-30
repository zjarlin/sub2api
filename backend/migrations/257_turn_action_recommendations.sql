-- 回合旁路推荐独立于 Responses 协议；只保存动作引用、判断与结构化特征。
CREATE TABLE IF NOT EXISTS turn_action_recommendations (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    session_id UUID NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    context_id VARCHAR(64) NOT NULL,
    input_digest VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (api_key_id, session_id, run_id, context_id)
);
CREATE INDEX IF NOT EXISTS idx_turn_action_recommendations_run
    ON turn_action_recommendations (api_key_id, session_id, run_id, updated_at DESC);
