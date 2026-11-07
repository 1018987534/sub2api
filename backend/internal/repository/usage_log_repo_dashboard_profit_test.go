//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepository_DashboardLotteryCostUsesCommittedLedgerAndLocalDay(t *testing.T) {
	for _, tc := range []struct {
		name  string
		zone  string
		month time.Month
		day   int
		hours int
	}{
		{"Shanghai", "Asia/Shanghai", time.November, 7, 24},
		{"DST spring", "America/New_York", time.March, 8, 23},
		{"DST fall", "America/New_York", time.November, 1, 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			require.NoError(t, err)
			start := time.Date(2026, tc.month, tc.day, 0, 0, 0, 0, loc)
			end := start.AddDate(0, 0, 1)
			require.Equal(t, time.Duration(tc.hours)*time.Hour, end.Sub(start))
			db, mock := newSQLMock(t)
			repo := &usageLogRepository{sql: db}
			mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(amount\), 0\).*FROM lottery_balance_ledger.*WHERE created_at >= \$1 AND created_at < \$2`).
				WithArgs(start, end).
				WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(7.5))
			stats := &DashboardStats{}
			require.NoError(t, repo.fillDashboardLotteryStats(context.Background(), stats, start))
			require.Equal(t, 7.5, stats.TodayLotteryCost)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageLogRepository_DashboardLotteryCostDoesNotHideLedgerErrors(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, time.November, 7, 0, 0, 0, 0, time.UTC)
	want := errors.New("lottery ledger unavailable")
	mock.ExpectQuery(`FROM lottery_balance_ledger`).
		WithArgs(start, start.AddDate(0, 0, 1)).WillReturnError(want)
	require.ErrorIs(t, repo.fillDashboardLotteryStats(context.Background(), &DashboardStats{}, start), want)
	require.NoError(t, mock.ExpectationsWereMet())
}
