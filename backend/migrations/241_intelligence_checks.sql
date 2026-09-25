-- Isolated V2 probes: official monitor tables and logic remain untouched.
CREATE TABLE IF NOT EXISTS intelligence_check_configs (
 group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
 version INTEGER NOT NULL DEFAULT 1, config JSONB NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 interval_minutes INTEGER NOT NULL CHECK (interval_minutes BETWEEN 1 AND 1440),
 timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds BETWEEN 5 AND 900),
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), lease_token TEXT, lease_until TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_intelligence_checks_due ON intelligence_check_configs(next_run_at) WHERE enabled;
CREATE TABLE IF NOT EXISTS intelligence_check_runs (
 id BIGSERIAL PRIMARY KEY, group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
 checked_at TIMESTAMPTZ NOT NULL, duration_ms BIGINT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('normal', 'degraded', 'error')),
 answer TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', config_snapshot JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_intelligence_runs_group_time ON intelligence_check_runs(group_id, checked_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_intelligence_runs_time ON intelligence_check_runs(checked_at);
