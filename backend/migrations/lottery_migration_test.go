package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLotteryMigration(t *testing.T) {
	content, err := FS.ReadFile("233_lottery.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "VALUES ('lottery_enabled', 'false')")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS lottery_config")
	require.Contains(t, sql, "draw_mode VARCHAR(16) NOT NULL DEFAULT 'auto'")
	require.Contains(t, sql, "next_round_mode VARCHAR(16) NOT NULL DEFAULT 'manual'")
	require.Contains(t, sql, "require_recharge BOOLEAN NOT NULL DEFAULT TRUE")
	require.Contains(t, sql, "draw_mode IN ('auto', 'manual')")
	require.Contains(t, sql, "next_round_mode IN ('auto', 'manual')")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_lottery_rounds_one_open")
	require.Contains(t, sql, "CHECK ((is_actor AND user_id IS NULL) OR (NOT is_actor AND user_id IS NOT NULL))")
	require.Contains(t, sql, "recent_recharge_days INTEGER NOT NULL DEFAULT 0")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS lottery_balance_ledger")
	require.Contains(t, sql, "winner_id BIGINT NOT NULL UNIQUE")
}

func TestLotteryManualProgressMigration(t *testing.T) {
	content, err := FS.ReadFile("234_lottery_manual_progress.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS manual_progress_count INTEGER NOT NULL DEFAULT 0")
	require.Contains(t, sql, "manual_progress_count = GREATEST(manual_progress_count, actor_count)")
	require.Contains(t, sql, "actor_target_count = 0")
	require.Contains(t, sql, "actor_count = 0")
	require.Contains(t, sql, "next_actor_at = NULL")
}
