-- Request-detail monitoring correlates ops_error_logs to Auto route observations by request_id.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_auto_model_routes_request_id
    ON auto_model_routes ((request_id::text), api_key_id);
