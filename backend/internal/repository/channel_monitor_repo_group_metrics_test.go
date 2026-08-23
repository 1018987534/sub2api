package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestListUserGroupMetricsAggregatesMonitoredGroups(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	mock.ExpectQuery(`(?s)WITH monitor_targets AS .*PERCENTILE_CONT.*FROM monitored_groups mg`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "platform", "id", "name", "first_token_p50_ms", "first_token_sample_count", "cache_read_tokens", "cache_rate_denominator",
		}).
			AddRow(int64(17), "openai", int64(10), "性价比分组", 1250.0, int64(24), int64(625), int64(1000)).
			AddRow(int64(15), "anthropic", int64(20), "KIRO分组", nil, nil, nil, nil))

	repo := &channelMonitorRepository{db: db}
	rows, err := repo.ListUserGroupMetrics(context.Background(), start, end)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "openai", rows[0].Platform)
	require.Equal(t, int64(17), rows[0].MonitorID)
	require.Equal(t, "性价比分组", rows[0].GroupName)
	require.NotNil(t, rows[0].FirstTokenP50Ms)
	require.Equal(t, int64(1250), *rows[0].FirstTokenP50Ms)
	require.NotNil(t, rows[0].CacheRate)
	require.InDelta(t, 0.625, *rows[0].CacheRate, 0.0001)
	require.Nil(t, rows[1].FirstTokenP50Ms)
	require.Zero(t, rows[1].FirstTokenSampleCount)
	require.Nil(t, rows[1].CacheRate)
	require.NoError(t, mock.ExpectationsWereMet())
}
