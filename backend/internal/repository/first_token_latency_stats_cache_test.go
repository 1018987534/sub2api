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

func TestFirstTokenLatencyStatsCacheRecordsRecentMedian(t *testing.T) {
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
	require.InDelta(t, 110, stats[42].PredictedMS, 0.001)
	require.Equal(t, "115", mr.HGet(firstTokenLatencyStatsPrefix+"42", "median_ms"))
	other, err := cache.GetStatsBatch(ctx, []int64{43})
	require.NoError(t, err)
	require.Equal(t, int64(1), other[43].SampleCount)
}

func TestFirstTokenLatencyStatsCacheRebuildsMedianFromAverageBasedSamples(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()
	key := firstTokenLatencyStatsPrefix + "44"
	require.NoError(t, rdb.HSet(ctx, key,
		"ewma_ms", "5000",
		"average_ms", "5000",
		"sample_count", "3",
		"updated_at_ms", strconv.FormatInt(time.Now().UnixMilli(), 10),
		"slow_streak", "0",
		"reliable_fast", "1",
		"recent_samples", "1000,5000,9000",
	).Err())

	stats, err := cache.GetStatsBatch(ctx, []int64{44})
	require.NoError(t, err)
	require.Equal(t, 5_000.0, stats[44].PredictedMS)
	require.True(t, stats[44].ReliableFast)
	require.True(t, stats[44].FastConfirmationTracked)
}

func TestFirstTokenLatencyStatsCacheDoesNotTrustSingleLegacyRecoverySample(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()
	key := firstTokenLatencyStatsPrefix + "45"
	require.NoError(t, rdb.HSet(ctx, key,
		"ewma_ms", "7000",
		"average_ms", "7000",
		"sample_count", "900",
		"updated_at_ms", strconv.FormatInt(time.Now().UnixMilli(), 10),
		"slow_streak", "0",
		"reliable_fast", "1",
		"recent_samples", "7000",
	).Err())

	stats, err := cache.GetStatsBatch(ctx, []int64{45})
	require.NoError(t, err)
	require.Equal(t, 7_000.0, stats[45].PredictedMS)
	require.False(t, stats[45].ReliableFast)
	require.True(t, stats[45].FastConfirmationTracked)

	require.NoError(t, cache.RecordSample(ctx, 45, "confirm-2", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{45})
	require.NoError(t, err)
	require.False(t, stats[45].ReliableFast)
	require.Equal(t, 1, stats[45].RecoveryFastStreak)

	require.NoError(t, cache.RecordSample(ctx, 45, "confirm-3", 9_000))
	require.NoError(t, cache.RecordSample(ctx, 45, "confirm-4", 10_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{45})
	require.NoError(t, err)
	require.True(t, stats[45].ReliableFast)
	require.Equal(t, 9_000.0, stats[45].PredictedMS)
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

func TestFirstTokenLatencyStatsCacheQueuesAndAtomicallyClaimsManualProbe(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	writer := &firstTokenLatencyStatsCache{rdb: rdb}
	reader := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	require.NoError(t, writer.RequestManualProbe(ctx, 12, 10*time.Minute))
	accountID, claimed, err := reader.TryClaimManualProbe(ctx, []int64{11, 12}, 10*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, int64(12), accountID)
	require.True(t, mr.Exists(firstTokenLatencyProbePrefix+"12"))

	accountID, claimed, err = writer.TryClaimManualProbe(ctx, []int64{11, 12}, 10*time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.Zero(t, accountID)

	// A real sample also clears a manual request that was queued while another
	// probe lease was active, avoiding an unnecessary second forced probe.
	require.NoError(t, writer.RequestManualProbe(ctx, 12, 10*time.Minute))
	require.NoError(t, reader.RecordSample(ctx, 12, "manual-probe-complete", 4_000))
	require.False(t, mr.Exists(firstTokenLatencyProbePrefix+"12"))
	require.False(t, mr.Exists(firstTokenLatencyManualProbePrefix+"12"))
}

func TestFirstTokenLatencyStatsCacheRequiresThreeConsecutiveFastRecoverySamples(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 4; index++ {
		require.NoError(t, cache.RecordSample(ctx, 13, fmt.Sprintf("slow-%d", index), 30_000))
	}
	require.NoError(t, cache.RecordSample(ctx, 13, "recovery-1", 5_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{13})
	require.NoError(t, err)
	require.Greater(t, stats[13].PredictedMS, 10_000.0)
	require.False(t, stats[13].ReliableFast)
	require.Equal(t, 1, stats[13].RecoveryFastStreak)

	require.NoError(t, cache.RecordSample(ctx, 13, "recovery-2", 7_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{13})
	require.NoError(t, err)
	require.Greater(t, stats[13].PredictedMS, 10_000.0)
	require.False(t, stats[13].ReliableFast)
	require.Equal(t, 2, stats[13].RecoveryFastStreak)

	require.NoError(t, cache.RecordSample(ctx, 13, "recovery-3", 9_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{13})
	require.NoError(t, err)
	require.Equal(t, 7_000.0, stats[13].PredictedMS)
	require.True(t, stats[13].ReliableFast)
	require.True(t, stats[13].FastConfirmationTracked)
	require.Zero(t, stats[13].RecoveryFastStreak)
	require.Zero(t, stats[13].SlowStreak)
}

func TestFirstTokenLatencyStatsCacheRecoveryStreakResetsAboveThreshold(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 4; index++ {
		require.NoError(t, cache.RecordSample(ctx, 14, fmt.Sprintf("slow-%d", index), 30_000))
	}
	require.NoError(t, cache.RecordSample(ctx, 14, "recovery-1", 10_000))
	require.NoError(t, cache.RecordSample(ctx, 14, "recovery-reset", 10_001))
	stats, err := cache.GetStatsBatch(ctx, []int64{14})
	require.NoError(t, err)
	require.Greater(t, stats[14].PredictedMS, 10_000.0)
	require.False(t, stats[14].ReliableFast)
	require.Zero(t, stats[14].RecoveryFastStreak)
}

func TestFirstTokenLatencyStatsCacheNewAccountNeedsThreeFastSamples(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	require.NoError(t, cache.RecordSample(ctx, 15, "fast-1", 7_500))
	stats, err := cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, 7_500.0, stats[15].PredictedMS)
	require.Equal(t, int64(1), stats[15].SampleCount)
	require.False(t, stats[15].ReliableFast)
	require.Equal(t, 1, stats[15].RecoveryFastStreak)
	require.NoError(t, cache.RecordSample(ctx, 15, "fast-2", 8_500))
	require.NoError(t, cache.RecordSample(ctx, 15, "fast-3", 9_500))
	stats, err = cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, 8_500.0, stats[15].PredictedMS)
	require.Equal(t, int64(3), stats[15].SampleCount)
	require.True(t, stats[15].ReliableFast)
}

func TestFirstTokenLatencyStatsCacheStableScoreIgnoresOrdinaryBurstNoise(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()

	for index := 0; index < 3; index++ {
		require.NoError(t, cache.RecordSample(ctx, 17, fmt.Sprintf("stable-%d", index), 20_000))
	}
	before, err := cache.GetStatsBatch(ctx, []int64{17})
	require.NoError(t, err)
	require.Equal(t, 20_000.0, before[17].PredictedMS)

	for index, latency := range []int{16_000, 24_000, 15_000, 25_000} {
		require.NoError(t, cache.RecordSample(ctx, 17, fmt.Sprintf("noise-%d", index), latency))
	}
	after, err := cache.GetStatsBatch(ctx, []int64{17})
	require.NoError(t, err)
	require.InDelta(t, before[17].PredictedMS, after[17].PredictedMS, 10)
}

func TestFirstTokenLatencyStatsCacheLeavesFastPoolOnlyAfterStablePredictionExceedsThreshold(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyStatsCache{rdb: rdb}
	ctx := context.Background()
	now := time.Unix(1_000, 0)
	mr.SetTime(now)

	for index := 0; index < 3; index++ {
		require.NoError(t, cache.RecordSample(ctx, 18, fmt.Sprintf("fast-%d", index), 6_000))
	}
	fast, err := cache.GetStatsBatch(ctx, []int64{18})
	require.NoError(t, err)
	require.True(t, fast[18].ReliableFast)

	require.NoError(t, cache.RecordSample(ctx, 18, "isolated-slow", 60_000))
	stillFast, err := cache.GetStatsBatch(ctx, []int64{18})
	require.NoError(t, err)
	require.True(t, stillFast[18].ReliableFast)
	require.Equal(t, 6_000.0, stillFast[18].PredictedMS)

	for index := 0; index < 3; index++ {
		now = now.Add(5 * time.Minute)
		mr.SetTime(now)
		require.NoError(t, cache.RecordSample(ctx, 18, fmt.Sprintf("sustained-slow-%d", index), 20_000))
	}
	slow, err := cache.GetStatsBatch(ctx, []int64{18})
	require.NoError(t, err)
	require.Greater(t, slow[18].PredictedMS, 10_000.0)
	require.False(t, slow[18].ReliableFast)
}

func TestFirstTokenLatencyStatsCacheFastProbeAfterStaleHistoryNeedsConfirmation(t *testing.T) {
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
	require.Equal(t, int64(1), stats[16].SampleCount)
	require.False(t, stats[16].ReliableFast)
	require.Equal(t, 1, stats[16].RecoveryFastStreak)
}
