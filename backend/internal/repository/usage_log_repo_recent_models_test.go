package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestListRecentModelsByGroupsUsesActualGroupUsage(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	mock.ExpectQuery(`(?s)SELECT DISTINCT ON.*ul\.group_id.*COALESCE\(NULLIF\(TRIM\(ul\.requested_model\), ''\), ul\.model\) AS model.*a\.platform.*COALESCE\(NULLIF\(TRIM\(ul\.upstream_model\), ''\), ul\.model\) AS upstream_model.*ul\.group_id = ANY\(\$1\).*ul\.created_at >= \$2.*ul\.created_at < \$3.*ul\.actual_cost > 0.*ul\.created_at DESC`).
		WithArgs(pq.Array([]int64{10, 20}), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "model", "platform", "upstream_model"}).
			AddRow(int64(10), "customer-alias", service.PlatformOpenAI, "gpt-live").
			AddRow(int64(20), "claude-used", service.PlatformAnthropic, "claude-upstream"))

	repo := &usageLogRepository{db: db}
	models, err := repo.ListRecentModelsByGroups(context.Background(), []int64{10, 20}, start, end)
	require.NoError(t, err)
	require.Equal(t, []service.RecentGroupModel{{
		Name: "customer-alias", Platform: service.PlatformOpenAI, UpstreamModel: "gpt-live",
	}}, models[10])
	require.Equal(t, []service.RecentGroupModel{{
		Name: "claude-used", Platform: service.PlatformAnthropic, UpstreamModel: "claude-upstream",
	}}, models[20])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListRecentModelsByGroupsSkipsMissingDatabase(t *testing.T) {
	repo := &usageLogRepository{}
	models, err := repo.ListRecentModelsByGroups(context.Background(), []int64{10}, time.Time{}, time.Now())
	require.NoError(t, err)
	require.Empty(t, models)
}
