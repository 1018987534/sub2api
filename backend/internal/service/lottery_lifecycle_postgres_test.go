package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Only run against a disposable local PostgreSQL database.
func TestLotteryLifecycleRealPostgres(t *testing.T) {
	dsn := os.Getenv("LOTTERY_TEST_DSN")
	if dsn == "" {
		t.Skip("set LOTTERY_TEST_DSN to a disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := fmt.Sprintf("lottery_test_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	_, err = db.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT,updated_at TIMESTAMPTZ DEFAULT NOW());
		CREATE TABLE users (id BIGINT PRIMARY KEY,email TEXT,username TEXT,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ,status TEXT DEFAULT 'active',balance NUMERIC DEFAULT 10);
		CREATE TABLE redeem_codes (used_by BIGINT,type TEXT,value NUMERIC,used_at TIMESTAMPTZ);
		INSERT INTO users (id,email) SELECT n,'fixture-'||n||'@example.test' FROM generate_series(1,7) n;`)
	require.NoError(t, err)
	for _, file := range []string{"233_lottery.sql", "234_lottery_manual_progress.sql", "244_lottery_paused_rounds.sql", "244_lottery_paused_rounds.sql"} {
		content, err := migrations.FS.ReadFile(file)
		require.NoError(t, err)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, string(content))
		if err != nil {
			_ = tx.Rollback()
		}
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
	}
	lottery := NewLotteryService(db, nil, nil)
	_, err = lottery.UpdateConfig(ctx, LotteryConfig{Enabled: true, ParticipantThreshold: 50, PrizeCount: 2, PrizeAmount: 5, DrawMode: "auto", NextRoundMode: "manual"})
	require.NoError(t, err)
	round, err := lottery.StartRound(ctx, 1)
	require.NoError(t, err)
	updated, err := lottery.UpdatePrizeCount(ctx, round.ID, 6)
	require.NoError(t, err)
	require.Equal(t, 6, updated.PrizeCount)
	cfg, err := lottery.GetConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, cfg.PrizeCount, "round edits must not modify defaults for future rounds")
	for userID := int64(1); userID <= 6; userID++ {
		_, err := lottery.Join(ctx, userID, "127.0.0.1")
		require.NoError(t, err)
	}
	paused, err := lottery.UpdateRoundStatus(ctx, round.ID, "paused")
	require.NoError(t, err)
	require.Equal(t, 6, paused.ParticipantCount)
	current, err := lottery.GetCurrent(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "paused", current.CurrentRound.Status)
	require.False(t, current.Eligibility.Eligible)
	_, err = lottery.Join(ctx, 7, "127.0.0.1")
	require.ErrorIs(t, err, ErrLotteryNoOpenRound)
	_, err = lottery.StartRound(ctx, 1)
	require.ErrorIs(t, err, ErrLotteryRoundAlreadyOpen)
	_, err = lottery.DrawRound(ctx, round.ID)
	require.Equal(t, "LOTTERY_ROUND_CLOSED", infraerrors.Reason(err))
	_, err = lottery.UpdateProgress(ctx, round.ID, 50)
	require.Equal(t, "LOTTERY_ROUND_CLOSED", infraerrors.Reason(err))
	require.NoError(t, lottery.Advance(ctx))
	_, err = lottery.UpdatePrizeCount(ctx, round.ID, 4)
	require.NoError(t, err)
	_, err = lottery.UpdatePrizeCount(ctx, round.ID, 6)
	require.NoError(t, err)
	resumed, err := lottery.UpdateRoundStatus(ctx, round.ID, "open")
	require.NoError(t, err)
	require.Equal(t, 6, resumed.ParticipantCount)
	result, err := lottery.DrawRound(ctx, round.ID)
	require.NoError(t, err)
	require.Len(t, result.Winners, 6)
	require.Equal(t, 6, result.Round.WinnerCount)
	t.Log("prize_count=2->6; paused blocks join/start/draw/progress/advance; resume preserves 6 entries; draw awards 6 winners")

	second, err := lottery.StartRound(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 2, second.PrizeCount)
	_, err = lottery.UpdateRoundStatus(ctx, second.ID, "paused")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE lottery_rounds SET status='open' WHERE id=$1`, round.ID)
	require.ErrorContains(t, err, "idx_lottery_rounds_one_active")
	voided, err := lottery.UpdateRoundStatus(ctx, second.ID, "cancelled")
	require.NoError(t, err)
	require.Zero(t, voided.WinnerCount)
	require.Nil(t, voided.DrawnAt)
	_, err = lottery.UpdateRoundStatus(ctx, second.ID, "open")
	require.Equal(t, "LOTTERY_STATUS_TRANSITION_INVALID", infraerrors.Reason(err))
	current, err = lottery.GetCurrent(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "cancelled", current.CurrentRound.Status)
	var awarded int
	var balance float64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lottery_winners`).Scan(&awarded))
	require.Equal(t, 6, awarded)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT SUM(balance) FROM users`).Scan(&balance))
	require.Equal(t, 100.0, balance)
	_, err = lottery.StartRound(ctx, 1)
	require.NoError(t, err)
	t.Log("paused->cancelled displayed as voided; no new winners/balance changes; cannot resume; active slot released; migration repeat passed")
}
