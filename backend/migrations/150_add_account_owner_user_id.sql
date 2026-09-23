-- Add owner_user_id to distinguish global admin accounts from user-owned private accounts.
-- NULL = global account; non-NULL = account contributed by and available only to that user.

ALTER TABLE accounts
  ADD COLUMN IF NOT EXISTS owner_user_id BIGINT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'accounts_owner_user_id_fkey'
  ) THEN
    ALTER TABLE accounts
      ADD CONSTRAINT accounts_owner_user_id_fkey
      FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE SET NULL;
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_accounts_owner_user_id
  ON accounts(owner_user_id)
  WHERE deleted_at IS NULL;
