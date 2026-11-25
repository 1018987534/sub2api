-- Keep the primary check table and its leases unchanged for rolling upgrades
-- and binary rollback. Old binaries can still write primary runs (probe_id=0).
CREATE TABLE IF NOT EXISTS intelligence_check_model_configs (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
 version INTEGER NOT NULL DEFAULT 1, config JSONB NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 interval_minutes INTEGER NOT NULL CHECK (interval_minutes BETWEEN 1 AND 1440),
 timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds BETWEEN 5 AND 900),
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), lease_token TEXT, lease_until TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_intelligence_model_configs_group ON intelligence_check_model_configs(group_id);
CREATE INDEX IF NOT EXISTS idx_intelligence_model_configs_due ON intelligence_check_model_configs(next_run_at) WHERE enabled;

ALTER TABLE intelligence_check_runs ADD COLUMN IF NOT EXISTS probe_id BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_intelligence_runs_probe ON intelligence_check_runs(group_id, probe_id, id DESC);
