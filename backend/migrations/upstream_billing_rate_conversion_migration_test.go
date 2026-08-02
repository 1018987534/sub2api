package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration194ClearsOnlyLegacySynchronizedSnapshotsAndRefreshesScheduler(t *testing.T) {
	content, err := FS.ReadFile("194_reset_legacy_upstream_billing_rate_snapshots.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "extra - 'upstream_billing_probe'")
	require.Contains(t, sql, `'{"upstream_billing_probe_enabled": true}'::jsonb`)
	require.Contains(t, sql, `'{"upstream_billing_rate_sync_enabled": true}'::jsonb`)
	require.Contains(t, sql, "rate_conversion_applied")
	require.Contains(t, sql, "IS DISTINCT FROM 'true'::jsonb")
	require.Contains(t, sql, "INSERT INTO scheduler_outbox")
	require.Contains(t, sql, "'account_changed'")
	require.NotContains(t, sql, "SET rate_multiplier")
}
