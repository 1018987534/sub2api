package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPrimaryBalanceNotifyEmailMigration(t *testing.T) {
	content, err := FS.ReadFile("236_default_primary_balance_notify_email.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "UPDATE users")
	require.Contains(t, sql, "jsonb_build_array")
	require.Contains(t, sql, "'email', BTRIM(email)")
	require.Contains(t, sql, "'disabled', false")
	require.Contains(t, sql, "'verified', true")
	require.Contains(t, sql, "WHERE deleted_at IS NULL")
	require.Contains(t, sql, "BTRIM(balance_notify_extra_emails) IN ('', '[]', 'null')")
	require.Contains(t, sql, "RETURNING id")
	require.Contains(t, sql, "INSERT INTO auth_cache_invalidation_outbox (cache_key)")
	require.Contains(t, sql, "JOIN updated_users AS u ON u.id = k.user_id")
	require.NotContains(t, sql, "balance_notify_enabled = true")
}
