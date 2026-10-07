package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func lotteryStatusConfigRows(enabled bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"enabled", "threshold", "prizes", "amount", "draw", "next", "recharge", "minimum", "age", "recent", "updated"}).
		AddRow(enabled, 50, 2, 5, "auto", "manual", true, 0, 0, 0, time.Now())
}

func TestLotteryUpdateRoundStatus(t *testing.T) {
	for _, tc := range []struct {
		from, to, reason string
		enabled          bool
	}{
		{"open", "paused", "", true},
		{"paused", "open", "", true},
		{"open", "cancelled", "", true},
		{"paused", "cancelled", "", true},
		{"open", "cancelled", "", false},
		{"paused", "open", "LOTTERY_DISABLED", false},
		{"drawn", "paused", "LOTTERY_STATUS_TRANSITION_INVALID", true},
		{"cancelled", "open", "LOTTERY_STATUS_TRANSITION_INVALID", true},
		{"open", "open", "LOTTERY_STATUS_TRANSITION_INVALID", true},
		{"paused", "paused", "LOTTERY_STATUS_TRANSITION_INVALID", true},
	} {
		t.Run(tc.from+"_to_"+tc.to+"_"+tc.reason, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery("FROM lottery_config WHERE id = 1 FOR UPDATE").WillReturnRows(lotteryStatusConfigRows(tc.enabled))
			mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows(tc.from, 6))
			if tc.reason == "" {
				mock.ExpectExec(`UPDATE lottery_rounds SET status=\$2,updated_at=NOW\(\) WHERE id=\$1`).WithArgs(int64(21), tc.to).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`WHERE r.id=\$1`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows(tc.to, 6))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			updated, err := NewLotteryService(db, nil, nil).UpdateRoundStatus(context.Background(), 21, tc.to)
			if tc.reason == "" {
				require.NoError(t, err)
				require.Equal(t, tc.to, updated.Status)
				require.Equal(t, 12, updated.ParticipantCount)
				require.Equal(t, 6, updated.PrizeCount)
				require.Zero(t, updated.WinnerCount)
			} else {
				require.Equal(t, tc.reason, infraerrors.Reason(err))
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLotteryUpdateRoundStatusRejectsArbitraryStatus(t *testing.T) {
	_, err := NewLotteryService(nil, nil, nil).UpdateRoundStatus(context.Background(), 21, "drawn")
	require.Equal(t, "LOTTERY_STATUS_INVALID", infraerrors.Reason(err))
}

func TestLotteryPausedRoundPreventsStartingAnother(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery("FROM lottery_config WHERE id = 1 FOR UPDATE").WillReturnRows(lotteryStatusConfigRows(true))
	mock.ExpectQuery(`SELECT id FROM lottery_rounds WHERE status IN \('open','paused'\) FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(21))
	mock.ExpectRollback()
	_, err = NewLotteryService(db, nil, nil).StartRound(context.Background(), 1)
	require.ErrorIs(t, err, ErrLotteryRoundAlreadyOpen)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLotteryPausedAndVoidedRoundsCannotDrawOrChangeProgress(t *testing.T) {
	for _, status := range []string{"paused", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery("FROM lottery_config WHERE id = 1 FOR UPDATE").WillReturnRows(lotteryStatusConfigRows(true))
			mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows(status, 6))
			mock.ExpectRollback()
			_, err = NewLotteryService(db, nil, nil).DrawRound(context.Background(), 21)
			require.Equal(t, "LOTTERY_ROUND_CLOSED", infraerrors.Reason(err))
			mock.ExpectBegin()
			mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows(status, 6))
			mock.ExpectRollback()
			_, err = NewLotteryService(db, nil, nil).UpdateProgress(context.Background(), 21, 12)
			require.Equal(t, "LOTTERY_ROUND_CLOSED", infraerrors.Reason(err))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLotteryPausedAndVoidedRoundsCannotJoin(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery("FROM lottery_config WHERE id = 1").WillReturnRows(lotteryStatusConfigRows(true))
	mock.ExpectQuery(`WHERE r.status='open' FOR UPDATE OF r`).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	_, err = NewLotteryService(db, nil, nil).Join(context.Background(), 1, "127.0.0.1")
	require.ErrorIs(t, err, ErrLotteryNoOpenRound)
	require.NoError(t, mock.ExpectationsWereMet())
}
