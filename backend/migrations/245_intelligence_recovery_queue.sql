-- Internal account recovery is separate from public group intelligence history.
CREATE TABLE IF NOT EXISTS intelligence_recovery_queue (
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 generation TEXT NOT NULL,
 retry_step INTEGER NOT NULL DEFAULT 0 CHECK (retry_step BETWEEN 0 AND 7),
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '3 minutes',
 lease_token TEXT,
 lease_until TIMESTAMPTZ,
 last_checked_at TIMESTAMPTZ,
 last_status TEXT CHECK (last_status IN ('normal', 'degraded', 'error'))
);
CREATE INDEX IF NOT EXISTS idx_intelligence_recovery_due ON intelligence_recovery_queue(next_run_at);
-- Adopt currently isolated accounts; expiry alone must not restore a degraded account.
UPDATE accounts SET extra=jsonb_set(extra,'{intelligence_recovery_required}','true')
 WHERE deleted_at IS NULL AND NULLIF(extra->>'intelligence_pause_until','') IS NOT NULL
 AND (extra->>'intelligence_recovery_required'='true'
      OR (extra->>'intelligence_pause_until')::timestamptz>NOW());
