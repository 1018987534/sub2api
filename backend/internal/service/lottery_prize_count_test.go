package service

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func lotteryPrizeTestRows(status string, prizeCount int) *sqlmock.Rows {
	now := time.Date(2026, 10, 7, 12, 30, 0, 0, time.UTC)
	return sqlmock.NewRows([]string{"id", "number", "status", "threshold", "prizes", "amount", "draw", "next", "recharge", "minimum", "age", "recent", "participants", "manual", "real", "winners", "ips", "started", "drawn", "updated"}).
		AddRow(21, 21, status, 50, prizeCount, 5, "auto", "manual", true, 0, 0, 0, 12, 0, 12, 0, 12, now, nil, now)
}

func TestLotteryUpdatePrizeCount(t *testing.T) {
	for _, counts := range [][2]int{{2, 6}, {6, 2}, {2, 2}, {2, 50}} {
		t.Run(fmt.Sprintf("%d_to_%d", counts[0], counts[1]), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows("open", counts[0]))
			mock.ExpectExec(`UPDATE lottery_rounds SET prize_count=\$2,updated_at=NOW\(\) WHERE id=\$1`).WithArgs(int64(21), counts[1]).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`WHERE r.id=\$1`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows("open", counts[1]))
			mock.ExpectCommit()

			updated, err := NewLotteryService(db, nil, nil).UpdatePrizeCount(context.Background(), 21, counts[1])
			require.NoError(t, err)
			require.Equal(t, counts[1], updated.PrizeCount)
			require.Equal(t, counts[1], toLotteryPublicRound(updated).PrizeCount)
			require.Equal(t, "open", updated.Status)
			require.Equal(t, 12, updated.ParticipantCount)
			require.Equal(t, 5.0, updated.PrizeAmount)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLotteryUpdatePrizeCountRejectsInvalidAndClosedRounds(t *testing.T) {
	for _, tc := range []struct {
		name, status, reason string
		count                int
	}{
		{"zero", "open", "LOTTERY_PRIZE_COUNT_INVALID", 0},
		{"negative", "open", "LOTTERY_PRIZE_COUNT_INVALID", -1},
		{"above_threshold", "open", "LOTTERY_PRIZE_COUNT_INVALID", 51},
		{"above_limit", "open", "LOTTERY_PRIZE_COUNT_INVALID", 10001},
		{"drawn", "drawn", "LOTTERY_ROUND_CLOSED", 6},
		{"cancelled", "cancelled", "LOTTERY_ROUND_CLOSED", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows(tc.status, 2))
			mock.ExpectRollback()
			_, err = NewLotteryService(db, nil, nil).UpdatePrizeCount(context.Background(), 21, tc.count)
			require.Equal(t, tc.reason, infraerrors.Reason(err))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLotteryUpdatePrizeCountMissingRound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	_, err = NewLotteryService(db, nil, nil).UpdatePrizeCount(context.Background(), 21, 6)
	require.ErrorIs(t, err, ErrLotteryRoundNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLotteryUpdatePrizeCountWhilePaused(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows("paused", 2))
	mock.ExpectExec(`UPDATE lottery_rounds SET prize_count`).WithArgs(int64(21), 6).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`WHERE r.id=\$1`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows("paused", 6))
	mock.ExpectCommit()
	updated, err := NewLotteryService(db, nil, nil).UpdatePrizeCount(context.Background(), 21, 6)
	require.NoError(t, err)
	require.Equal(t, "paused", updated.Status)
	require.Equal(t, 6, updated.PrizeCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLotteryUpdatePrizeCountWriteFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery(`WHERE r.id=\$1 FOR UPDATE OF r`).WithArgs(int64(21)).WillReturnRows(lotteryPrizeTestRows("open", 2))
	mock.ExpectExec(`UPDATE lottery_rounds SET prize_count`).WithArgs(int64(21), 6).WillReturnError(fmt.Errorf("fixture write failure"))
	mock.ExpectRollback()
	_, err = NewLotteryService(db, nil, nil).UpdatePrizeCount(context.Background(), 21, 6)
	require.ErrorContains(t, err, "fixture write failure")
	require.NoError(t, mock.ExpectationsWereMet())
}
