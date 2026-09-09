package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestLotteryCurrentPreservesCompletedRound(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(map[bool]string{false: "not joined", true: "joined"}[joined], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			now := time.Now()
			mock.ExpectQuery("FROM lottery_config WHERE id = 1").WillReturnRows(
				sqlmock.NewRows([]string{"enabled", "threshold", "prizes", "amount", "draw", "next", "recharge", "minimum", "age", "recent", "updated"}).
					AddRow(true, 50, 2, 5, "auto", "manual", false, 0, 0, 0, now))
			mock.ExpectQuery("FROM lottery_rounds r WHERE r.status='open'").WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery("FROM lottery_rounds r WHERE r.status='drawn' ORDER BY r.round_no DESC LIMIT 1").WillReturnRows(
				sqlmock.NewRows([]string{"id", "number", "status", "threshold", "prizes", "amount", "draw", "next", "recharge", "minimum", "age", "recent", "participants", "manual", "real", "winners", "ips", "started", "drawn", "updated"}).
					AddRow(10, 10, "drawn", 50, 2, 5, "auto", "manual", false, 0, 0, 0, 50, 0, 50, 2, 50, now, now, now))
			mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(10), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(joined))
			columns := []string{"id", "round_id", "round_no", "email", "amount", "awarded", "joined"}
			mock.ExpectQuery("FROM lottery_winners.*ORDER BY").WithArgs(lotteryRecentWinnerLimit).WillReturnRows(sqlmock.NewRows(columns).AddRow(1, 10, 10, "winner@example.com", 5, now, now))
			mock.ExpectQuery("FROM lottery_winners.*WHERE w.user_id").WithArgs(int64(7), 20).WillReturnRows(sqlmock.NewRows(columns).AddRow(1, 10, 10, "winner@example.com", 5, now, now))

			current, err := NewLotteryService(db, nil, nil).GetCurrent(context.Background(), 7)
			require.NoError(t, err)
			require.NotNil(t, current.CurrentRound)
			require.Equal(t, int64(10), current.CurrentRound.RoundNo)
			require.Equal(t, "drawn", current.CurrentRound.Status)
			require.Equal(t, 50, current.CurrentRound.ParticipantCount)
			require.Equal(t, joined, current.Joined)
			require.False(t, current.Eligibility.Eligible)
			require.Len(t, current.RecentWinners, 1)
			require.Len(t, current.MyRecentWinners, 1)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLotteryCurrentWithoutAnyRound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("FROM lottery_config WHERE id = 1").WillReturnRows(
		sqlmock.NewRows([]string{"enabled", "threshold", "prizes", "amount", "draw", "next", "recharge", "minimum", "age", "recent", "updated"}).AddRow(true, 50, 2, 5, "auto", "manual", false, 0, 0, 0, time.Now()))
	mock.ExpectQuery("FROM lottery_rounds r WHERE r.status='open'").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("FROM lottery_rounds r WHERE r.status='drawn' ORDER BY r.round_no DESC LIMIT 1").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("FROM lottery_winners").WithArgs(lotteryRecentWinnerLimit).WillReturnRows(sqlmock.NewRows([]string{"id", "round_id", "round_no", "email", "amount", "awarded", "joined"}))
	current, err := NewLotteryService(db, nil, nil).GetCurrent(context.Background(), 0)
	require.NoError(t, err)
	require.Nil(t, current.CurrentRound)
	require.Empty(t, current.RecentWinners)
	require.NoError(t, mock.ExpectationsWereMet())
}
