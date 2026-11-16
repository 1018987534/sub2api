package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// GetDashboardAdminAccountCosts uses the same stored per-request cost snapshots
// as dashboard upstream cost, including upstream costs on zero-billed requests.
// Current account pricing and user actual_cost must not reconstruct these costs.
func (r *usageLogRepository) GetDashboardAdminAccountCosts(ctx context.Context, adminID int64, at time.Time) (today, month float64, err error) {
	local := at.In(timezone.Location())
	dayStart := timezone.StartOfDay(local)
	monthStart := timezone.StartOfMonth(local)
	query := `SELECT
		COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1))
			FILTER (WHERE created_at >= $3), 0),
		COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0)
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $4`
	err = scanSingleRow(ctx, r.sql, query, []any{adminID, monthStart, dayStart, dayStart.AddDate(0, 0, 1)}, &today, &month)
	return today, month, err
}
