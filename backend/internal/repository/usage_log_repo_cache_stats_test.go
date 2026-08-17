//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetAccountCacheStatsBatchAggregatesCacheReadRateInputs(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT")).WithArgs(int64(11), int64(12), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "cache_read_tokens", "cache_rate_denominator"}).
			AddRow(int64(11), int64(75), int64(300)).
			AddRow(int64(12), int64(0), int64(0)))

	stats, err := repo.GetAccountCacheStatsBatch(context.Background(), []int64{11, 12}, start, end)

	require.NoError(t, err)
	require.Equal(t, map[int64]service.AccountCacheStats{
		11: {CacheReadTokens: 75, CacheRateDenominator: 300},
		12: {CacheReadTokens: 0, CacheRateDenominator: 0},
	}, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountCacheStatsBatchReturnsEmptyForNoAccounts(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	stats, err := repo.GetAccountCacheStatsBatch(context.Background(), nil, time.Time{}, time.Time{})

	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}
