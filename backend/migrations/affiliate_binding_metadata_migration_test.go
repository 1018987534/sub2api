//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAffiliateBindingMetadataMigration(t *testing.T) {
	t.Parallel()
	raw, err := FS.ReadFile("177_affiliate_binding_metadata.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "add column if not exists inviter_bound_at")
	require.Contains(t, sql, "add column if not exists inviter_bind_source")
	require.Contains(t, sql, "where inviter_id is not null")
	require.Contains(t, sql, "'registration'")
}
