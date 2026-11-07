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

func TestUsageLogRepository_DashboardQQRewardCostUsesPaidRewardsAndLocalDay(t *testing.T) {
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
			mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(amount\), 0\).*FROM qq_activity_rewards\s+WHERE status = 'paid' AND paid_at >= \$1 AND paid_at < \$2`).
				WithArgs(start, end).
				WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(4))
			stats := &DashboardStats{}
			require.NoError(t, repo.fillDashboardQQRewardStats(context.Background(), stats, start))
			require.Equal(t, 4.0, stats.TodayQQRewardCost)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageLogRepository_DashboardQQRewardCostEmptyDay(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, time.November, 7, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`FROM qq_activity_rewards`).
		WithArgs(start, start.AddDate(0, 0, 1)).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(0))
	stats := &DashboardStats{TodayQQRewardCost: 4}
	require.NoError(t, repo.fillDashboardQQRewardStats(context.Background(), stats, start))
	require.Zero(t, stats.TodayQQRewardCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepository_DashboardQQRewardCostDoesNotHideLedgerErrors(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, time.November, 7, 0, 0, 0, 0, time.UTC)
	want := errors.New("QQ reward ledger unavailable")
	mock.ExpectQuery(`FROM qq_activity_rewards`).
		WithArgs(start, start.AddDate(0, 0, 1)).WillReturnError(want)
	require.ErrorIs(t, repo.fillDashboardQQRewardStats(context.Background(), &DashboardStats{}, start), want)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepository_DashboardMonthStatsUsesLocalMonthAndBothSources(t *testing.T) {
	for _, tc := range []struct {
		name, zone string
		at         time.Time
		first, end string
	}{
		{"Shanghai", "Asia/Shanghai", time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), "2026-11-01", "2026-11-09"},
		{"DST day", "America/New_York", time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), "2026-03-01", "2026-03-09"},
		{"local month boundary", "Asia/Shanghai", time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), "2026-11-01", "2026-11-02"},
	} {
		for _, aggregated := range []bool{true, false} {
			t.Run(tc.name+map[bool]string{true: " aggregated", false: " raw"}[aggregated], func(t *testing.T) {
				loc, err := time.LoadLocation(tc.zone)
				require.NoError(t, err)
				local := tc.at.In(loc)
				today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
				first, err := time.ParseInLocation("2006-01-02", tc.first, loc)
				require.NoError(t, err)
				end, err := time.ParseInLocation("2006-01-02", tc.end, loc)
				require.NoError(t, err)
				db, mock := newSQLMock(t)
				if aggregated {
					mock.ExpectQuery(`(?s)SUM\(actual_cost\).*SUM\(account_cost\).*FROM usage_dashboard_daily WHERE bucket_date >= \$1::date AND bucket_date < \$2::date`).WithArgs(tc.first, tc.end).WillReturnRows(sqlmock.NewRows([]string{"actual", "cost"}).AddRow(1000, 600))
				} else {
					mock.ExpectQuery(`(?s)SUM\(actual_cost\).*COALESCE\(account_stats_cost, total_cost\).*COALESCE\(account_rate_multiplier, 1\).*FROM usage_logs WHERE created_at >= \$1 AND created_at < \$2`).WithArgs(first, end).WillReturnRows(sqlmock.NewRows([]string{"actual", "cost"}).AddRow(1000, 600))
				}
				mock.ExpectQuery(`(?s)FROM lottery_balance_ledger.*WHERE created_at >= \$1 AND created_at < \$2`).WithArgs(first, end).WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(10))
				mock.ExpectQuery(`(?s)FROM qq_activity_rewards.*WHERE status = 'paid' AND paid_at >= \$1 AND paid_at < \$2`).WithArgs(first, end).WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(20))
				stats := &DashboardStats{TodayActualCost: 315.88}
				require.NoError(t, (&usageLogRepository{sql: db}).fillDashboardMonthStats(context.Background(), stats, today, aggregated))
				require.Equal(t, 1000.0, stats.MonthActualCost)
				require.Equal(t, 600.0, stats.MonthAccountCost)
				require.Equal(t, 10.0, stats.MonthLotteryCost)
				require.Equal(t, 20.0, stats.MonthQQRewardCost)
				require.Equal(t, 315.88, stats.TodayActualCost)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestUsageLogRepository_DashboardMonthStatsDoesNotHideErrors(t *testing.T) {
	for _, source := range []string{"usage", "lottery", "QQ"} {
		t.Run(source, func(t *testing.T) {
			db, mock := newSQLMock(t)
			want := errors.New(source + " unavailable")
			usage := mock.ExpectQuery(`FROM usage_dashboard_daily`)
			if source == "usage" {
				usage.WillReturnError(want)
			} else {
				usage.WillReturnRows(sqlmock.NewRows([]string{"actual", "cost"}).AddRow(0, 0))
				lottery := mock.ExpectQuery(`FROM lottery_balance_ledger`)
				if source == "lottery" {
					lottery.WillReturnError(want)
				} else {
					lottery.WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(0))
					mock.ExpectQuery(`FROM qq_activity_rewards`).WillReturnError(want)
				}
			}
			require.ErrorIs(t, (&usageLogRepository{sql: db}).fillDashboardMonthStats(context.Background(), &DashboardStats{}, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), true), want)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
