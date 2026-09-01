package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newTotalLatencyTestCache(t *testing.T) (*miniredis.Miniredis, *redis.Client, *firstTokenLatencyStatsCache) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb, &firstTokenLatencyStatsCache{rdb: rdb}
}

func recordTotalLatencySamples(t *testing.T, cache *firstTokenLatencyStatsCache, accountID int64, prefix string, values []int) {
	t.Helper()
	for index, durationMS := range values {
		require.NoError(t, cache.RecordSample(context.Background(), accountID, fmt.Sprintf("%s-%d", prefix, index), durationMS))
	}
}

func repeatedDurations(count, durationMS int) []int {
	values := make([]int, count)
	for index := range values {
		values[index] = durationMS
	}
	return values
}

func TestTotalLatencyStatsCacheRequiresTenSamplesAndThreeFastAggregates(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()

	recordTotalLatencySamples(t, cache, 15, "bootstrap", repeatedDurations(9, 8_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, int64(9), stats[15].SampleCount)
	require.Zero(t, stats[15].PredictedMS)
	require.False(t, stats[15].ReliableFast)

	require.NoError(t, cache.RecordSample(ctx, 15, "fast-10", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, 8_000.0, stats[15].PredictedMS)
	require.Equal(t, 1, stats[15].RecoveryFastStreak)
	require.False(t, stats[15].ReliableFast)

	require.NoError(t, cache.RecordSample(ctx, 15, "fast-11", 8_000))
	require.NoError(t, cache.RecordSample(ctx, 15, "fast-12", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.True(t, stats[15].ReliableFast)
	require.Zero(t, stats[15].RecoveryFastStreak)
}

func TestTotalLatencyStatsCacheUsesLatestTenMedianAndPercentiles(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	values := make([]int, 0, 10)
	for seconds := 1; seconds <= 10; seconds++ {
		values = append(values, seconds*1_000)
	}
	recordTotalLatencySamples(t, cache, 42, "range", values)

	stats, err := cache.GetStatsBatch(context.Background(), []int64{42})
	require.NoError(t, err)
	require.InDelta(t, 5_500, stats[42].PredictedMS, 0.001)
	require.InDelta(t, 5_500, stats[42].P50MS, 0.001)
	require.InDelta(t, 9_100, stats[42].P90MS, 0.001)
	require.Equal(t, 10, stats[42].SampleWindowSize)
}

func TestTotalLatencyStatsCacheDeduplicatesPerAccountRequest(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.RecordSample(ctx, 42, "same-request", 8_000))
	require.NoError(t, cache.RecordSample(ctx, 42, "same-request", 90_000))
	require.NoError(t, cache.RecordSample(ctx, 43, "same-request", 9_000))

	stats, err := cache.GetStatsBatch(ctx, []int64{42, 43})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats[42].SampleCount)
	require.Equal(t, int64(1), stats[43].SampleCount)
}

func TestTotalLatencyStatsCacheSingleSlowGenerationDoesNotEvictFastAccount(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	recordTotalLatencySamples(t, cache, 17, "fast", repeatedDurations(22, 8_000))

	before, err := cache.GetStatsBatch(ctx, []int64{17})
	require.NoError(t, err)
	require.True(t, before[17].ReliableFast)

	require.NoError(t, cache.RecordSample(ctx, 17, "one-long-valid-generation", 300_000))
	after, err := cache.GetStatsBatch(ctx, []int64{17})
	require.NoError(t, err)
	require.True(t, after[17].ReliableFast)
	require.Equal(t, 8_000.0, after[17].PredictedMS)
	require.Zero(t, after[17].SlowStreak)
}

func TestTotalLatencyStatsCacheFastAccountExitsAfterShortSlowBurst(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(18)
	recordTotalLatencySamples(t, cache, accountID, "fast", repeatedDurations(22, 8_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast)

	for index := 1; index <= 2; index++ {
		require.NoError(t, cache.RecordSample(ctx, accountID, fmt.Sprintf("slow-%d", index), 20_000))
		stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
		require.NoError(t, err)
		require.True(t, stats[accountID].ReliableFast, "two slow requests are not enough for the short-window confirmation")
	}

	require.NoError(t, cache.RecordSample(ctx, accountID, "slow-3", 20_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast)
	require.Equal(t, 3, stats[accountID].SlowStreak)
}

func TestTotalLatencyStatsCacheShortWindowIncludesMoreThanLatestTenRequests(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(24)
	recordTotalLatencySamples(t, cache, accountID, "bootstrap", repeatedDurations(12, 8_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast)

	// Add two fast requests after three slow requests. The latest-ten median
	// is fast again, but the five-minute burst still contains three consecutive
	// slow completions and must remove the account from the fast pool.
	recordTotalLatencySamples(t, cache, accountID, "slow-burst", repeatedDurations(3, 20_000))
	recordTotalLatencySamples(t, cache, accountID, "fast-tail", repeatedDurations(2, 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast)
	require.Equal(t, 3, stats[accountID].SlowStreak)
}

func TestTotalLatencyStatsCacheKeepsOnlyLatestTenRequests(t *testing.T) {
	_, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(19)
	values := make([]int, 0, 15)
	for i := 1; i <= 15; i++ {
		values = append(values, i*1_000)
	}
	recordTotalLatencySamples(t, cache, accountID, "latest", values)

	samplesKey := fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID)
	count, err := rdb.ZCard(ctx, samplesKey).Result()
	require.NoError(t, err)
	require.Equal(t, int64(10), count)
	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.Equal(t, int64(10), stats[accountID].SampleCount)
	require.Equal(t, 10_500.0, stats[accountID].PredictedMS)
	require.Equal(t, 10, stats[accountID].SampleWindowSize)
}

func TestTotalLatencyStatsCacheStaleFastStateRequiresFreshConfirmation(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	recordTotalLatencySamples(t, cache, 20, "fast", repeatedDurations(22, 8_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{20})
	require.NoError(t, err)
	require.True(t, stats[20].ReliableFast)

	mr.SetTime(stats[20].UpdatedAt.Add(6*time.Hour + time.Second))
	require.NoError(t, cache.RecordSample(ctx, 20, "after-stale-gap", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{20})
	require.NoError(t, err)
	require.False(t, stats[20].ReliableFast)
	require.Equal(t, 1, stats[20].RecoveryFastStreak)
}

func TestTotalLatencyStatsCacheUsesIndependentRedisNamespace(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	mr.HSet("scheduler:first_token:account:21", "ewma_ms", "1000", "reliable_fast", "1")

	stats, err := cache.GetStatsBatch(ctx, []int64{21})
	require.NoError(t, err)
	require.NotContains(t, stats, int64(21))

	require.NoError(t, cache.RecordSample(ctx, 21, "duration", 9_000))
	require.True(t, mr.Exists(totalLatencyStatsPrefix+"21"))
	require.Equal(t, "1000", mr.HGet("scheduler:first_token:account:21", "ewma_ms"))
}

func TestTotalLatencyStatsCacheProbeLeaseClearsAfterSample(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	claimed, err := cache.TryClaimProbe(ctx, 22, 10*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.TryClaimProbe(ctx, 22, 10*time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)

	require.NoError(t, cache.RecordSample(ctx, 22, "probe-complete", 9_000))
	require.False(t, mr.Exists(totalLatencyProbePrefix+"22"))
	claimed, err = cache.TryClaimProbe(ctx, 22, 10*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
}

func TestTotalLatencyStatsCacheQueuesAndAtomicallyClaimsManualProbe(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.RequestManualProbe(ctx, 23, 10*time.Minute))
	accountID, claimed, err := cache.TryClaimManualProbe(ctx, []int64{22, 23}, 10*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, int64(23), accountID)
	require.True(t, mr.Exists(totalLatencyProbePrefix+"23"))
	require.False(t, mr.Exists(totalLatencyManualProbePrefix+"23"))
}
