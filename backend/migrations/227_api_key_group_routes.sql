ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS group_routes JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN api_keys.group_routes IS
    'Ordered group routes for this API key; first item is primary and max_rate_multiplier null means unlimited';
