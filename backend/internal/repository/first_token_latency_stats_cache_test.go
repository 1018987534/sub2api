package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
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

func TestTotalLatencyStatsCacheRequiresTwentySamplesAndThreeFastAggregates(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()

	recordTotalLatencySamples(t, cache, 15, "bootstrap", repeatedDurations(19, 8_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, int64(19), stats[15].SampleCount)
	require.Zero(t, stats[15].PredictedMS)
	require.False(t, stats[15].ReliableFast)

	require.NoError(t, cache.RecordSample(ctx, 15, "fast-20", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, 8_000.0, stats[15].PredictedMS)
	require.Equal(t, 1, stats[15].RecoveryFastStreak)
	require.False(t, stats[15].ReliableFast)

	require.NoError(t, cache.RecordSample(ctx, 15, "fast-21", 8_000))
	require.NoError(t, cache.RecordSample(ctx, 15, "fast-22", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.True(t, stats[15].ReliableFast)
	require.Zero(t, stats[15].RecoveryFastStreak)
}

func TestTotalLatencyStatsCacheUsesTenToNinetyTrimmedMeanAndPercentiles(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	values := make([]int, 0, 20)
	for seconds := 1; seconds <= 20; seconds++ {
		values = append(values, seconds*1_000)
	}
	recordTotalLatencySamples(t, cache, 42, "range", values)

	stats, err := cache.GetStatsBatch(context.Background(), []int64{42})
	require.NoError(t, err)
	require.InDelta(t, 10_500, stats[42].PredictedMS, 0.001)
	require.InDelta(t, 10_500, stats[42].P50MS, 0.001)
	require.InDelta(t, 18_100, stats[42].P90MS, 0.001)
	require.Equal(t, 6, stats[42].WindowHours)
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

func TestTotalLatencyStatsCacheAggregatesDimensionsAtAccountScope(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	fast := service.TotalDurationLatencyDimension{AccountID: 50, RequestedModel: "gpt-5.5", ReasoningEffort: "high"}
	slow := service.TotalDurationLatencyDimension{AccountID: 50, RequestedModel: "gpt-5.5", ReasoningEffort: "low"}
	for index := 0; index < 20; index++ {
		require.NoError(t, cache.RecordSampleForDimension(ctx, fast, fmt.Sprintf("fast-%d", index), 8_000))
		require.NoError(t, cache.RecordSampleForDimension(ctx, slow, fmt.Sprintf("slow-%d", index), 75_000))
	}
	stats, err := cache.GetStatsBatchForDimensions(ctx, []service.TotalDurationLatencyDimension{fast, slow})
	require.NoError(t, err)
	normalized := service.NormalizeTotalDurationLatencyDimension(fast)
	require.Len(t, stats, 1)
	require.Equal(t, 41_500.0, stats[normalized].PredictedMS)
	require.Empty(t, normalized.RequestedModel)
	require.Empty(t, normalized.ReasoningEffort)
	metrics, err := cache.ListStatsByAccountIDs(ctx, []int64{50})
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Empty(t, metrics[0].Dimension.RequestedModel)
	require.Empty(t, metrics[0].Dimension.ReasoningEffort)
}

func TestTotalLatencyStatsCacheSingleLongGenerationDoesNotEvictFastAccount(t *testing.T) {
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

func TestTotalLatencyStatsCacheFastAccountNeedsThreeSlowAggregatesToExit(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(18)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	samplesKey := fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID)
	nowMS := time.Now().UnixMilli()
	require.NoError(t, rdb.HSet(ctx, statsKey,
		"is_fast", "1",
		"updated_at_ms", fmt.Sprint(nowMS),
		"enter_fast_streak", "0",
		"exit_slow_streak", "0",
	).Err())
	for index := 0; index < 19; index++ {
		member := fmt.Sprintf("%d:seed-%d:22000", nowMS, index)
		require.NoError(t, rdb.ZAdd(ctx, samplesKey, redis.Z{Score: float64(nowMS), Member: member}).Err())
	}

	for index := 1; index <= 2; index++ {
		require.NoError(t, cache.RecordSample(ctx, accountID, fmt.Sprintf("slow-%d", index), 22_000))
		stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
		require.NoError(t, err)
		require.True(t, stats[accountID].ReliableFast)
		require.Equal(t, index, stats[accountID].SlowStreak)
	}
	require.NoError(t, cache.RecordSample(ctx, accountID, "slow-3", 22_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast)
	require.Equal(t, 3, stats[accountID].SlowStreak)
	require.True(t, mr.Exists(statsKey))
}

func TestTotalLatencyStatsCacheTwentyOneSecondBoundaryDoesNotExitFastPool(t *testing.T) {
	_, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(181)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	samplesKey := fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID)
	nowMS := time.Now().UnixMilli()
	require.NoError(t, rdb.HSet(ctx, statsKey,
		"is_fast", "1",
		"updated_at_ms", fmt.Sprint(nowMS),
		"enter_fast_streak", "0",
		"exit_slow_streak", "0",
	).Err())
	for index := 0; index < 19; index++ {
		member := fmt.Sprintf("%d:seed-%d:21000", nowMS, index)
		require.NoError(t, rdb.ZAdd(ctx, samplesKey, redis.Z{Score: float64(nowMS), Member: member}).Err())
	}

	require.NoError(t, cache.RecordSample(ctx, accountID, "boundary-21s", 21_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast)
	require.Zero(t, stats[accountID].SlowStreak)
}

func TestTotalLatencyStatsCacheRecentMinutePlusRatioImmediatelyExitsFastPool(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(182)
	recordTotalLatencySamples(t, cache, accountID, "fast", repeatedDurations(22, 8_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast)

	for index := 0; index < 12; index++ {
		require.NoError(t, cache.RecordSample(ctx, accountID, fmt.Sprintf("recent-slow-%d", index), 61_000+index))
	}
	stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast, "more than 35%% minute-plus requests must immediately leave fast pool")
	require.Equal(t, int64(0), stats[accountID].SampleCount, "reset dimensions must return to pending collection")
	require.False(t, mr.Exists(fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID)), "reset must drop stale samples")
}

func TestTotalLatencyStatsCacheSingleFiveMinuteRequestImmediatelyBreaksAffinity(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(183)
	recordTotalLatencySamples(t, cache, accountID, "fast", repeatedDurations(22, 8_000))
	before, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, before[accountID].ReliableFast)

	probeKey := fmt.Sprintf("%s%d", totalLatencyProbePrefix, accountID)
	require.NoError(t, rdb.Set(ctx, probeKey, "1", time.Minute).Err())
	require.NoError(t, cache.RecordSample(ctx, accountID, "five-minute-request", 300_001))

	after, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.Equal(t, int64(0), after[accountID].SampleCount)
	require.False(t, after[accountID].ReliableFast)
	require.True(t, after[accountID].CircuitBroken)
	require.False(t, mr.Exists(fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID)))
	require.False(t, mr.Exists(probeKey), "circuit break must release probe leases")

	require.NoError(t, cache.RecordSample(ctx, accountID, "recovery-sample", 8_000))
	recovered, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.Equal(t, int64(1), recovered[accountID].SampleCount)
	require.False(t, recovered[accountID].CircuitBroken, "a new sample clears the pending circuit marker")
}

func TestTotalLatencyStatsCacheFallsBackToTwentyFourHours(t *testing.T) {
	_, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(19)
	recordTotalLatencySamples(t, cache, accountID, "older", repeatedDurations(20, 10_000))

	samplesKey := fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID)
	members, err := rdb.ZRange(ctx, samplesKey, 0, -1).Result()
	require.NoError(t, err)
	olderScore := float64(time.Now().Add(-7 * time.Hour).UnixMilli())
	for _, member := range members {
		require.NoError(t, rdb.ZAddArgs(ctx, samplesKey, redis.ZAddArgs{XX: true, Members: []redis.Z{{Score: olderScore, Member: member}}}).Err())
	}
	require.NoError(t, cache.RecordSample(ctx, accountID, "fresh", 10_000))

	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.Equal(t, 24, stats[accountID].WindowHours)
	require.Equal(t, int64(21), stats[accountID].SampleCount)
	require.Equal(t, 10_000.0, stats[accountID].PredictedMS)
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
