package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupRoutesMigration(t *testing.T) {
	content, err := FS.ReadFile("229_api_key_group_routes.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS group_routes JSONB NOT NULL DEFAULT '[]'::jsonb")
	require.Contains(t, sql, "COMMENT ON COLUMN api_keys.group_routes")
	require.Contains(t, sql, "Ordered group routes")
}
