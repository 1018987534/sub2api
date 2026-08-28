package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestLotteryListParticipantsReturnsRealUsersInJoinOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	joinedAt := time.Date(2026, 8, 28, 10, 30, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT COUNT\(p.id\).*FROM lottery_rounds r.*p.is_actor=FALSE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(`SELECT p.id,p.round_id,p.user_id.*FROM lottery_participants p.*p.is_actor=FALSE.*ORDER BY p.joined_at ASC,p.id ASC`).
		WithArgs(int64(7), 2, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "round_id", "user_id", "username", "email", "client_ip", "joined_at"}).
			AddRow(int64(103), int64(7), int64(9003), "alice", "alice@example.com", "203.0.113.3", joinedAt))

	service := NewLotteryService(db, nil, nil)
	page, err := service.ListParticipants(context.Background(), 7, 2, 2)
	require.NoError(t, err)
	require.Equal(t, int64(3), page.Total)
	require.Equal(t, 2, page.Page)
	require.Equal(t, 2, page.Pages)
	require.Equal(t, []LotteryParticipant{{
		ID: 103, RoundID: 7, UserID: 9003, Username: "alice", Email: "alice@example.com",
		ClientIP: "203.0.113.3", JoinedAt: joinedAt,
	}}, page.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLotteryListParticipantsRejectsMissingRound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`SELECT COUNT\(p.id\).*FROM lottery_rounds r`).
		WithArgs(int64(99)).
		WillReturnError(sql.ErrNoRows)

	service := NewLotteryService(db, nil, nil)
	_, err = service.ListParticipants(context.Background(), 99, 1, 20)
	require.ErrorIs(t, err, ErrLotteryRoundNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
