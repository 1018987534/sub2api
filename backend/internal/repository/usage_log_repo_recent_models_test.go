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

func TestListRecentModelsByGroupRestrictsSchedulableAccounts(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	mock.ExpectQuery(`(?s)SELECT DISTINCT.*COALESCE\(NULLIF\(TRIM\(ul\.requested_model\), ''\), ul\.model\) AS model.*a\.platform.*COALESCE\(NULLIF\(TRIM\(ul\.upstream_model\), ''\), ul\.model\) AS upstream_model.*ul\.account_id = ANY\(\$2\).*ul\.actual_cost > 0`).
		WithArgs(int64(10), pq.Array([]int64{101, 102}), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"model", "platform", "upstream_model"}).
			AddRow("customer-alias", service.PlatformOpenAI, "gpt-live"))

	repo := &usageLogRepository{db: db}
	models, err := repo.ListRecentModelsByGroup(context.Background(), 10, []int64{101, 102}, start, end)
	require.NoError(t, err)
	require.Equal(t, []service.RecentGroupModel{{
		Name: "customer-alias", Platform: service.PlatformOpenAI, UpstreamModel: "gpt-live",
	}}, models)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListRecentModelsByGroupSkipsEmptyAccountSet(t *testing.T) {
	repo := &usageLogRepository{}
	models, err := repo.ListRecentModelsByGroup(context.Background(), 10, nil, time.Time{}, time.Now())
	require.NoError(t, err)
	require.Empty(t, models)
}
