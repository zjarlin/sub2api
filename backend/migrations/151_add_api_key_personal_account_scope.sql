-- Add a per-API-key private account dispatch scope.
-- When enabled, the key dispatches only to accounts owned by the key user.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS personal_account_scope BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_api_keys_personal_account_scope
    ON api_keys(personal_account_scope)
    WHERE deleted_at IS NULL AND personal_account_scope = TRUE;
