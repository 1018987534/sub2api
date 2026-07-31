CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_instance_created
    ON usage_logs (instance_id, created_at DESC)
    WHERE instance_id IS NOT NULL;
