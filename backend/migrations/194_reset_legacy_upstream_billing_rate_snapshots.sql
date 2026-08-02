-- Snapshots created before converted-rate semantics store the upstream
-- declaration in resolved_rate_multiplier. Clear only those synchronized
-- snapshots so the probe runner rebuilds them with one canonical local rate;
-- accounts.rate_multiplier remains unchanged until that successful probe.
WITH reset_accounts AS (
    UPDATE accounts
    SET extra = extra - 'upstream_billing_probe',
        updated_at = NOW()
    WHERE deleted_at IS NULL
      AND type = 'apikey'
      AND extra @> '{"upstream_billing_probe_enabled": true}'::jsonb
      AND extra @> '{"upstream_billing_rate_sync_enabled": true}'::jsonb
      AND extra ? 'upstream_billing_probe'
      AND COALESCE(
          extra #> '{upstream_billing_probe,data,rate_conversion_applied}',
          'false'::jsonb
      ) IS DISTINCT FROM 'true'::jsonb
    RETURNING id
)
INSERT INTO scheduler_outbox (event_type, account_id)
SELECT 'account_changed', id
FROM reset_accounts;
