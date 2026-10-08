-- Admin-only audit trail; never queried by customer channel intelligence status.
CREATE TABLE IF NOT EXISTS intelligence_recovery_runs (
 id BIGSERIAL PRIMARY KEY,
 lease_token TEXT NOT NULL UNIQUE,
 account_id BIGINT NOT NULL,
 account_name TEXT NOT NULL,
 group_id BIGINT NOT NULL,
 group_name TEXT NOT NULL,
 model TEXT NOT NULL,
 reasoning_effort TEXT NOT NULL,
 protocol TEXT NOT NULL,
 retry_step INTEGER NOT NULL CHECK (retry_step BETWEEN 0 AND 7),
 started_at TIMESTAMPTZ NOT NULL,
 lease_until TIMESTAMPTZ NOT NULL,
 finished_at TIMESTAMPTZ,
 duration_ms BIGINT NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','normal','degraded','error')),
 outcome TEXT NOT NULL DEFAULT 'running' CHECK (outcome IN ('running','recovered','retry','superseded','interrupted','recovery_failed'))
);
CREATE INDEX IF NOT EXISTS idx_intelligence_recovery_history ON intelligence_recovery_runs(started_at);
