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

func TestListRecentModelsByGroupsUsesLatestFinalDispatch(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	mock.ExpectQuery(`(?s)WITH latest_requested AS.*SELECT DISTINCT ON.*ul\.group_id.*COALESCE\(NULLIF\(TRIM\(ul\.requested_model\), ''\), ul\.model\).*COALESCE\(NULLIF\(TRIM\(ul\.upstream_model\), ''\), ul\.model\) AS final_model.*ul\.group_id = ANY\(\$1\).*ul\.created_at >= \$2.*ul\.created_at < \$3.*ul\.actual_cost > 0.*ul\.created_at DESC.*SELECT DISTINCT group_id, final_model AS model`).
		WithArgs(pq.Array([]int64{10, 20}), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "model", "platform", "upstream_model"}).
			AddRow(int64(10), "gpt-5.6-terra", service.PlatformOpenAI, "gpt-5.6-terra").
			AddRow(int64(20), "claude-upstream", service.PlatformAnthropic, "claude-upstream"))

	repo := &usageLogRepository{db: db}
	models, err := repo.ListRecentModelsByGroups(context.Background(), []int64{10, 20}, start, end)
	require.NoError(t, err)
	require.Equal(t, []service.RecentGroupModel{{
		Name: "gpt-5.6-terra", Platform: service.PlatformOpenAI, UpstreamModel: "gpt-5.6-terra",
	}}, models[10])
	require.NotContains(t, models[10], service.RecentGroupModel{
		Name: "gpt-5.6-luna", Platform: service.PlatformOpenAI, UpstreamModel: "gpt-5.6-luna",
	})
	require.Equal(t, []service.RecentGroupModel{{
		Name: "claude-upstream", Platform: service.PlatformAnthropic, UpstreamModel: "claude-upstream",
	}}, models[20])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListRecentModelsByGroupsSkipsMissingDatabase(t *testing.T) {
	repo := &usageLogRepository{}
	models, err := repo.ListRecentModelsByGroups(context.Background(), []int64{10}, time.Time{}, time.Now())
	require.NoError(t, err)
	require.Empty(t, models)
}
