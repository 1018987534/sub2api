package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListLatestPerModelScansFailureThreshold(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	checkedAt := time.Now().UTC()
	mock.ExpectQuery(`(?s)WITH latest AS \(.*failure_threshold_reached`).
		WithArgs(int64(42), service.MonitorFailureConfirmationThreshold).
		WillReturnRows(sqlmock.NewRows([]string{
			"model", "status", "latency_ms", "ping_latency_ms", "checked_at", "failure_threshold_reached",
		}).AddRow("gpt-5.6-sol", service.MonitorStatusError, 250, nil, checkedAt, true))

	repo := &channelMonitorRepository{db: db}
	rows, err := repo.ListLatestPerModel(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "gpt-5.6-sol", rows[0].Model)
	require.True(t, rows[0].FailureThresholdReached)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListLatestForMonitorIDsScansUnconfirmedFailure(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	checkedAt := time.Now().UTC()
	mock.ExpectQuery(`(?s)WITH latest AS \(.*failure_threshold_reached`).
		WithArgs(sqlmock.AnyArg(), service.MonitorFailureConfirmationThreshold).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "model", "status", "latency_ms", "ping_latency_ms", "checked_at", "quota", "failure_threshold_reached",
		}).AddRow(int64(7), "gpt-5.6-sol", service.MonitorStatusFailed, 180, 20, checkedAt, nil, false))

	repo := &channelMonitorRepository{db: db}
	rows, err := repo.ListLatestForMonitorIDs(context.Background(), []int64{7})
	require.NoError(t, err)
	require.Len(t, rows[7], 1)
	require.False(t, rows[7][0].FailureThresholdReached)
	require.NoError(t, mock.ExpectationsWereMet())
}
