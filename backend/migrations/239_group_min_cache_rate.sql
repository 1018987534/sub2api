-- Per-group minimum cache hit rate for total-duration account scheduling.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS min_cache_rate DECIMAL(10,4) NOT NULL DEFAULT 0;
