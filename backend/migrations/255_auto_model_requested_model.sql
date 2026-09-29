ALTER TABLE auto_model_routes
    ADD COLUMN IF NOT EXISTS requested_model TEXT NOT NULL DEFAULT 'auto';
