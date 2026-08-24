package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorSortOrderMigration(t *testing.T) {
	content, err := FS.ReadFile("232_channel_monitor_sort_order.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 1000000")
	require.Contains(t, sql, "ROW_NUMBER() OVER (ORDER BY id ASC) * 10")
	require.Contains(t, sql, "monitor.sort_order = 1000000")
	require.Contains(t, sql, "idx_channel_monitors_sort_order")
}
