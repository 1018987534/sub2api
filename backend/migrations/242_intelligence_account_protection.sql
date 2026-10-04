ALTER TABLE intelligence_check_runs
    ADD COLUMN IF NOT EXISTS account_id BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS paused BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_intelligence_runs_account
    ON intelligence_check_runs(group_id, account_id, id DESC);
