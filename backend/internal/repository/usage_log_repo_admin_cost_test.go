//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepository_DashboardAdminAccountCostsUsesStoredCostAndLocalCalendar(t *testing.T) {
	require.NoError(t, timezone.Init("Asia/Shanghai"))
	t.Cleanup(func() { require.NoError(t, timezone.Init("Asia/Shanghai")) })
	loc := timezone.Location()
	for _, at := range []time.Time{
		time.Date(2026, 11, 16, 18, 49, 0, 0, loc),
		time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC),
	} {
		t.Run(at.Format(time.RFC3339), func(t *testing.T) {
			db, mock := newSQLMock(t)
			day := timezone.StartOfDay(at.In(loc))
			month := timezone.StartOfMonth(at.In(loc))
			// Exact SQL deliberately has no current-account JOIN, actual_cost
			// predicate, request-type restriction, or two-decimal rounding.
			mock.ExpectQuery(`(?s)^SELECT\s+COALESCE\(SUM\(COALESCE\(account_stats_cost, total_cost\) \* COALESCE\(account_rate_multiplier, 1\)\)\s+FILTER \(WHERE created_at >= \$3\), 0\),\s+COALESCE\(SUM\(COALESCE\(account_stats_cost, total_cost\) \* COALESCE\(account_rate_multiplier, 1\)\), 0\)\s+FROM usage_logs\s+WHERE user_id = \$1 AND created_at >= \$2 AND created_at < \$4$`).
				WithArgs(int64(7), month, day, day.AddDate(0, 0, 1)).
				WillReturnRows(sqlmock.NewRows([]string{"today", "month"}).AddRow(0.81256789, 78.987654))
			today, monthly, err := (&usageLogRepository{sql: db}).GetDashboardAdminAccountCosts(context.Background(), 7, at)
			require.NoError(t, err)
			require.Equal(t, 0.81256789, today)
			require.Equal(t, 78.987654, monthly)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageLogRepository_DashboardAdminAccountCostsDoesNotHideQueryErrors(t *testing.T) {
	db, mock := newSQLMock(t)
	want := errors.New("cost ledger unavailable")
	mock.ExpectQuery("FROM usage_logs").WillReturnError(want)
	_, _, err := (&usageLogRepository{sql: db}).GetDashboardAdminAccountCosts(context.Background(), 7, time.Now())
	require.ErrorIs(t, err, want)
	require.NoError(t, mock.ExpectationsWereMet())
}
