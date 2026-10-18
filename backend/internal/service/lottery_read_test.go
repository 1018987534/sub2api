package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestLotteryCurrentReadReminders(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		latestWinner, readWinner int64
		wantUnread               int64
	}{
		{"never won", 0, 0, 0},
		{"unread prize", 9, 0, 9},
		{"read prize", 9, 9, 0},
		{"new prize after read", 10, 9, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery("FROM lottery_config WHERE id = 1").WillReturnRows(lotteryStatusConfigRows(true))
			mock.ExpectQuery(`FROM lottery_rounds r WHERE r.status IN \('open','paused'\)`).WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery(`FROM lottery_rounds r WHERE r.status IN \('drawn','cancelled'\)`).WillReturnError(sql.ErrNoRows)
			columns := []string{"id", "round_id", "round_no", "email", "amount", "awarded", "joined"}
			// A public winner must never be interpreted as this user's prize.
			mock.ExpectQuery("FROM lottery_winners.*ORDER BY").WithArgs(lotteryRecentWinnerLimit).WillReturnRows(sqlmock.NewRows(columns).AddRow(99, 10, 10, "other@example.test", 5, time.Now(), time.Now()))
			mock.ExpectQuery("FROM lottery_winners.*WHERE w.user_id").WithArgs(int64(571), 20).WillReturnRows(sqlmock.NewRows(columns))
			mock.ExpectQuery("SELECT COALESCE\\(MAX\\(id\\),0\\)").WithArgs(int64(571)).WillReturnRows(sqlmock.NewRows([]string{"latest_winner", "read_round", "read_winner"}).AddRow(tc.latestWinner, 10, tc.readWinner))
			current, err := NewLotteryService(db, nil, nil).GetCurrent(context.Background(), 571)
			require.NoError(t, err)
			require.Equal(t, tc.wantUnread, current.UnreadWinnerID)
			require.False(t, current.CurrentRoundUnread)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLotteryMarkReadRejectsInvalidIDs(t *testing.T) {
	for _, ids := range [][3]int64{{0, 1, 1}, {7, -1, 1}, {7, 1, -1}, {7, 0, 0}} {
		require.Error(t, NewLotteryService(nil, nil, nil).MarkRead(context.Background(), ids[0], ids[1], ids[2]))
	}
}

func TestLotteryReadLifecycleRealPostgres(t *testing.T) {
	svc, cfg, ctx := newLotteryEligibilityFixture(t)
	cfg.PrizeCount = 2
	_, err := svc.UpdateConfig(ctx, cfg)
	require.NoError(t, err)
	round, err := svc.StartRound(ctx, 1)
	require.NoError(t, err)
	current, err := svc.GetCurrent(ctx, 1)
	require.NoError(t, err)
	require.True(t, current.CurrentRoundUnread)
	require.False(t, current.Joined)
	require.Zero(t, current.UnreadWinnerID)
	require.NoError(t, svc.MarkRead(ctx, 1, round.ID, 0))
	current, err = svc.GetCurrent(ctx, 1)
	require.NoError(t, err)
	require.False(t, current.CurrentRoundUnread)
	require.False(t, current.Joined, "reading must not enter the draw")
	other, err := svc.GetCurrent(ctx, 2)
	require.NoError(t, err)
	require.True(t, other.CurrentRoundUnread, "read state belongs only to its owner")
	for _, userID := range []int64{1, 2} {
		_, err = svc.Join(ctx, userID, "127.0.0.1")
		require.NoError(t, err)
	}
	_, err = svc.DrawRound(ctx, round.ID)
	require.NoError(t, err)
	current, err = svc.GetCurrent(ctx, 1)
	require.NoError(t, err)
	oldWinner := current.UnreadWinnerID
	require.Positive(t, oldWinner)
	require.NoError(t, svc.MarkRead(ctx, 1, round.ID, oldWinner))
	current, err = svc.GetCurrent(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, current.UnreadWinnerID)
	require.Len(t, current.MyRecentWinners, 1, "reading retains the prize history")
	other, err = svc.GetCurrent(ctx, 2)
	require.NoError(t, err)
	require.Positive(t, other.UnreadWinnerID)

	next, err := svc.StartRound(ctx, 1)
	require.NoError(t, err)
	_, err = svc.UpdatePrizeCount(ctx, next.ID, 1)
	require.NoError(t, err)
	_, err = svc.Join(ctx, 1, "127.0.0.1")
	require.NoError(t, err)
	_, err = svc.DrawRound(ctx, next.ID)
	require.NoError(t, err)
	require.NoError(t, svc.MarkRead(ctx, 1, round.ID, oldWinner))
	current, err = svc.GetCurrent(ctx, 1)
	require.NoError(t, err)
	require.True(t, current.CurrentRoundUnread, "late acknowledgement must not consume a new round")
	require.Greater(t, current.UnreadWinnerID, oldWinner, "late acknowledgement must not consume a new prize")

	// Exercise the actual upgrade against existing prize history, including
	// repeat application without resurrecting read notifications.
	_, err = svc.db.ExecContext(ctx, `DROP TABLE lottery_read_state`)
	require.NoError(t, err)
	content, err := migrations.FS.ReadFile("247_lottery_read_state.sql")
	require.NoError(t, err)
	_, err = svc.db.ExecContext(ctx, string(content))
	require.NoError(t, err)
	other, err = svc.GetCurrent(ctx, 2)
	require.NoError(t, err)
	require.Zero(t, other.UnreadWinnerID, "old draws must not produce a fresh prize reminder on upgrade")
	current, err = svc.GetCurrent(ctx, 1)
	require.NoError(t, err)
	require.Positive(t, current.UnreadWinnerID, "the latest draw keeps its reminder")
	var before, after float64
	require.NoError(t, svc.db.QueryRowContext(ctx, `SELECT SUM(balance) FROM users`).Scan(&before))
	require.NoError(t, svc.MarkRead(ctx, 1, next.ID, current.UnreadWinnerID))
	require.NoError(t, svc.MarkRead(ctx, 1, round.ID, oldWinner))
	_, err = svc.db.ExecContext(ctx, string(content))
	require.NoError(t, err)
	current, err = svc.GetCurrent(ctx, 1)
	require.NoError(t, err)
	require.False(t, current.CurrentRoundUnread)
	require.Zero(t, current.UnreadWinnerID)
	require.NoError(t, svc.db.QueryRowContext(ctx, `SELECT SUM(balance) FROM users`).Scan(&after))
	require.Equal(t, before, after, "acknowledging reminders never changes money")
	t.Log("round dot: unread -> read without joining -> next round unread; prize: unread -> read -> new prize unread; account isolation, late acknowledgements, upgrade backfill, repeat migration and balances verified")
}
