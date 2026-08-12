package repository

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestFirstTokenLatencyStatsCacheRecordsRobustSharedPrediction(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index, latency := range []int{100, 110, 120, 10000} {
		require.NoError(t, cache.RecordSample(ctx, 42, "request-"+string(rune('a'+index)), latency))
	}
	// A repeated billing callback for the same request must not skew the metric.
	require.NoError(t, cache.RecordSample(ctx, 42, "request-a", 50000))
	// The same inbound request may fail over to another account. Dedupe is
	// per-account so both upstream observations remain useful.
	require.NoError(t, cache.RecordSample(ctx, 43, "request-a", 500))

	stats, err := cache.GetStatsBatch(ctx, []int64{42, 99})
	require.NoError(t, err)
	require.NotContains(t, stats, int64(99))
	require.Equal(t, int64(4), stats[42].SampleCount)
	require.Greater(t, stats[42].PredictedMS, float64(100))
	require.Less(t, stats[42].PredictedMS, float64(2000), "one tail sample must not dominate the robust prediction")
	other, err := cache.GetStatsBatch(ctx, []int64{43})
	require.NoError(t, err)
	require.Equal(t, int64(1), other[43].SampleCount)
}

func TestFirstTokenLatencyStatsCacheSharesStatsAcrossInstancesAndResetsSlowStreak(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	writer := &firstTokenLatencyStatsCache{rdb: rdb}
	reader := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 3; index++ {
		require.NoError(t, writer.RecordSample(ctx, 7, fmt.Sprintf("slow-%d", index), 10_000))
	}
	before, err := reader.GetStatsBatch(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, int64(3), before[7].SampleCount)
	require.Positive(t, before[7].SlowStreak)

	require.NoError(t, writer.RecordSample(ctx, 7, "recovered", 1_000))
	after, err := reader.GetStatsBatch(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, int64(4), after[7].SampleCount)
	require.Zero(t, after[7].SlowStreak)
}

func TestFirstTokenLatencyStatsCacheRequiresFreshSamplesAfterLongGap(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 3; index++ {
		require.NoError(t, cache.RecordSample(ctx, 9, fmt.Sprintf("before-gap-%d", index), 8_000))
	}
	statsKey := fmt.Sprintf("%s%d", firstTokenLatencyStatsPrefix, 9)
	staleUpdatedAt := time.Now().Add(-21 * time.Minute).UnixMilli()
	require.NoError(t, rdb.HSet(ctx, statsKey, "updated_at_ms", strconv.FormatInt(staleUpdatedAt, 10)).Err())

	require.NoError(t, cache.RecordSample(ctx, 9, "after-gap", 1_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{9})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats[9].SampleCount)
	require.Zero(t, stats[9].SlowStreak)
}

func TestFirstTokenLatencyStatsCacheProbeLeaseClearsAfterSample(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	claimed, err := cache.TryClaimProbe(ctx, 12, 10*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.TryClaimProbe(ctx, 12, 10*time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)

	require.NoError(t, cache.RecordSample(ctx, 12, "probe-complete", 4_000))
	claimed, err = cache.TryClaimProbe(ctx, 12, 10*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
}

func TestFirstTokenLatencyStatsCacheFastRecoveryResetsPredictionImmediately(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 4; index++ {
		require.NoError(t, cache.RecordSample(ctx, 13, fmt.Sprintf("slow-%d", index), 30_000))
	}
	require.NoError(t, cache.RecordSample(ctx, 13, "recovered-under-threshold", 5_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{13})
	require.NoError(t, err)
	require.Less(t, stats[13].PredictedMS, 10_000.0)
	require.GreaterOrEqual(t, stats[13].SampleCount, int64(3))
	require.Zero(t, stats[13].SlowStreak)
}

func TestFirstTokenLatencyStatsCacheTenSecondRecoveryResetsPredictionImmediately(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 4; index++ {
		require.NoError(t, cache.RecordSample(ctx, 14, fmt.Sprintf("slow-%d", index), 30_000))
	}
	require.NoError(t, cache.RecordSample(ctx, 14, "recovered-at-threshold", 10_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{14})
	require.NoError(t, err)
	require.LessOrEqual(t, stats[14].PredictedMS, 10_000.0)
	require.GreaterOrEqual(t, stats[14].SampleCount, int64(3))
	require.Zero(t, stats[14].SlowStreak)
}

func TestFirstTokenLatencyStatsCacheFirstFastProbeIsImmediatelyReliable(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	require.NoError(t, cache.RecordSample(ctx, 15, "first-fast-probe", 7_500))
	stats, err := cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, 7_500.0, stats[15].PredictedMS)
	require.GreaterOrEqual(t, stats[15].SampleCount, int64(3))
	require.Zero(t, stats[15].SlowStreak)
}

func TestFirstTokenLatencyStatsCacheFastProbeAfterStaleHistoryIsImmediatelyReliable(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 3; index++ {
		require.NoError(t, cache.RecordSample(ctx, 16, fmt.Sprintf("old-%d", index), 8_000))
	}
	statsKey := fmt.Sprintf("%s%d", firstTokenLatencyStatsPrefix, 16)
	staleUpdatedAt := time.Now().Add(-21 * time.Minute).UnixMilli()
	require.NoError(t, rdb.HSet(ctx, statsKey, "updated_at_ms", strconv.FormatInt(staleUpdatedAt, 10)).Err())

	require.NoError(t, cache.RecordSample(ctx, 16, "stale-fast-probe", 9_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{16})
	require.NoError(t, err)
	require.Equal(t, 9_000.0, stats[16].PredictedMS)
	require.GreaterOrEqual(t, stats[16].SampleCount, int64(3))
}
