package repository

import (
	"context"
	"fmt"
	"strings"
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

func recordSustainedFastTotalLatencySamples(
	t *testing.T,
	mr *miniredis.Miniredis,
	cache *firstTokenLatencyStatsCache,
	accountID int64,
	prefix string,
	base time.Time,
) {
	t.Helper()
	for index := 0; index < 20; index++ {
		mr.SetTime(base.Add(time.Duration(index) * 4 * time.Second))
		require.NoError(t, cache.RecordSample(context.Background(), accountID, fmt.Sprintf("%s-%d", prefix, index), 8_000))
	}
}

func TestTotalLatencyStatsCacheRequiresTwentySamplesAndSustainedFastWindow(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	base := time.Unix(1_699_900_000, 0)
	mr.SetTime(base)

	recordTotalLatencySamples(t, cache, 15, "bootstrap", repeatedDurations(19, 8_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, int64(19), stats[15].SampleCount)
	require.Equal(t, 8_000.0, stats[15].PredictedMS)
	require.False(t, stats[15].ReliableFast)

	mr.SetTime(base.Add(30 * time.Second))
	require.NoError(t, cache.RecordSample(ctx, 15, "fast-20", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.Equal(t, 8_000.0, stats[15].PredictedMS)
	require.Equal(t, 20, stats[15].RecoveryFastStreak)
	require.False(t, stats[15].ReliableFast)

	mr.SetTime(base.Add(time.Minute))
	require.NoError(t, cache.RecordSample(ctx, 15, "fast-21", 8_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{15})
	require.NoError(t, err)
	require.True(t, stats[15].ReliableFast)
	require.Zero(t, stats[15].RecoveryFastStreak)
}

func TestTotalLatencyStatsCacheUsesAverageOfLatestFiftySamples(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	for index := 0; index < 60; index++ {
		// Make the first ten observations old and very slow. They must not be
		// included once the latest-50 bound is applied.
		mr.SetTime(time.UnixMilli(int64(index + 1)))
		durationMS := 1_000_000
		if index >= 10 {
			durationMS = (index - 9) * 1_000
		}
		require.NoError(t, cache.RecordSample(ctx, 42, fmt.Sprintf("range-%d", index), durationMS))
	}

	stats, err := cache.GetStatsBatch(ctx, []int64{42})
	require.NoError(t, err)
	require.Equal(t, int64(50), stats[42].SampleCount)
	require.InDelta(t, 25_500, stats[42].PredictedMS, 0.001)
	require.InDelta(t, 25_500, stats[42].P50MS, 0.001)
	require.InDelta(t, 45_100, stats[42].P90MS, 0.001)
	require.Equal(t, 6, stats[42].WindowHours)
}

func TestTotalLatencyStatsCacheUsesAllSamplesBelowFifty(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	values := []int{100_000, 2_000, 4_000}
	recordTotalLatencySamples(t, cache, 43, "short", values)

	stats, err := cache.GetStatsBatch(context.Background(), []int64{43})
	require.NoError(t, err)
	require.Equal(t, int64(3), stats[43].SampleCount)
	require.Equal(t, 35_333.333333333336, stats[43].PredictedMS)
}

func TestTotalLatencyStatsCacheDoesNotTrimExtremesForAverage(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	values := make([]int, 0, 20)
	values = append(values, 1_000)
	for index := 2; index <= 17; index++ {
		values = append(values, index*1_000)
	}
	values = append(values, 50_000, 100_000, 1_000_000)
	recordTotalLatencySamples(t, cache, 44, "extremes", values)

	stats, err := cache.GetStatsBatch(context.Background(), []int64{44})
	require.NoError(t, err)
	require.Equal(t, int64(20), stats[44].SampleCount)
	// The configured score keeps all samples and uses their arithmetic average.
	require.Equal(t, 65_150.0, stats[44].PredictedMS)
	require.Equal(t, 10_500.0, stats[44].P50MS)
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

func TestTotalLatencyStatsCacheRecordFailureRemovesFastPoolAndRaisesPrediction(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(45)
	base := time.Unix(1_699_960_000, 0)
	recordSustainedFastTotalLatencySamples(t, mr, cache, accountID, "fast", base)

	before, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, before[accountID].ReliableFast)

	mr.SetTime(base.Add(2 * time.Minute))
	require.NoError(t, cache.RecordFailure(ctx, accountID, "failed-request", 60_001))
	after, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, after[accountID].ReliableFast)
	require.GreaterOrEqual(t, after[accountID].PredictedMS, 60_001.0)
	require.False(t, after[accountID].CircuitBroken, "semantic failures use the slow-pool staircase instead of the rapid circuit probe")

	// The same upstream request can reach more than one terminal-event handler;
	// account/request dedupe must keep it to one observation.
	require.NoError(t, cache.RecordFailure(ctx, accountID, "failed-request", 90_001))
	afterDuplicate, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.Equal(t, after[accountID].SampleCount, afterDuplicate[accountID].SampleCount)
}

func TestTotalLatencyStatsCacheSingleSampleCircuitUsesStrictGreaterThanBoundary(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	base := time.Unix(1_699_950_000, 0)
	recordSustainedFastTotalLatencySamples(t, mr, cache, 17, "fast", base)

	before, err := cache.GetStatsBatch(ctx, []int64{17})
	require.NoError(t, err)
	require.True(t, before[17].ReliableFast)

	mr.SetTime(base.Add(90 * time.Second))
	require.NoError(t, cache.RecordSample(ctx, 17, "circuit-boundary", 60_000))
	after, err := cache.GetStatsBatch(ctx, []int64{17})
	require.NoError(t, err)
	require.True(t, after[17].ReliableFast)

	mr.SetTime(base.Add(91 * time.Second))
	require.NoError(t, cache.RecordSample(ctx, 17, "circuit-trigger", 60_001))
	after, err = cache.GetStatsBatch(ctx, []int64{17})
	require.NoError(t, err)
	require.False(t, after[17].ReliableFast, "one completed request strictly above the circuit threshold must evict the fast account")
	require.True(t, after[17].CircuitBroken)
	require.Zero(t, after[17].SlowStreak)
}

func TestTotalLatencyStatsCacheFastAccountRequiresThreeMinuteLargeAreaDegradationToExit(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(18)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	base := time.Unix(1_700_000_000, 0)
	recordSustainedFastTotalLatencySamples(t, mr, cache, accountID, "fast", base)

	mr.SetTime(base.Add(4 * time.Minute))
	for index := 0; index < 9; index++ {
		require.NoError(t, cache.RecordSample(ctx, accountID, fmt.Sprintf("short-burst-%d", index), 20_000))
	}
	mr.SetTime(base.Add(6*time.Minute - time.Second))
	require.NoError(t, cache.RecordSample(ctx, accountID, "short-burst-9", 20_000))

	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast, "ten slow requests compressed into less than two minutes must not evict the fast account")
	require.Equal(t, "10", rdb.HGet(ctx, statsKey, "exit_window_sample_count").Val())
	require.Equal(t, "10", rdb.HGet(ctx, statsKey, "exit_window_slow_count").Val())
	require.Equal(t, "0", rdb.HGet(ctx, statsKey, "exit_window_degraded").Val())

	mr.SetTime(base.Add(6*time.Minute + time.Second))
	require.NoError(t, cache.RecordSample(ctx, accountID, "sustained-slow", 20_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast)
	require.Equal(t, 11, stats[accountID].SlowStreak)
	require.Equal(t, "1", rdb.HGet(ctx, statsKey, "exit_window_degraded").Val())
	require.Equal(t, "8", rdb.HGet(ctx, statsKey, "score_version").Val())
	require.True(t, mr.Exists(statsKey))
}

func TestTotalLatencyStatsCacheFastExitDoesNotRequireTenTransitionSamples(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(28)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	base := time.Unix(1_700_050_000, 0)
	recordSustainedFastTotalLatencySamples(t, mr, cache, accountID, "fast", base)

	mr.SetTime(base.Add(4 * time.Minute))
	require.NoError(t, cache.RecordSample(ctx, accountID, "slow-start", 20_000))
	mr.SetTime(base.Add(6*time.Minute + time.Second))
	require.NoError(t, cache.RecordSample(ctx, accountID, "slow-confirm", 20_000))

	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast, "two slow samples spanning more than two minutes are sufficient once the ten-sample gate is removed")
	require.Equal(t, "2", rdb.HGet(ctx, statsKey, "exit_window_sample_count").Val())
	require.Equal(t, "2", rdb.HGet(ctx, statsKey, "exit_window_slow_count").Val())
	require.Equal(t, "1", rdb.HGet(ctx, statsKey, "exit_window_degraded").Val())
}

func TestTotalLatencyStatsCacheFastExitRequiresSeventyPercentSlowRequests(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(24)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	base := time.Unix(1_700_100_000, 0)
	recordSustainedFastTotalLatencySamples(t, mr, cache, accountID, "fast", base)

	mr.SetTime(base.Add(4 * time.Minute))
	recordTotalLatencySamples(t, cache, accountID, "recent-fast", repeatedDurations(4, 8_000))
	recordTotalLatencySamples(t, cache, accountID, "recent-slow", repeatedDurations(5, 20_000))
	mr.SetTime(base.Add(6*time.Minute + time.Second))
	require.NoError(t, cache.RecordSample(ctx, accountID, "recent-slow-5", 20_000))

	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast, "six of ten slow requests is not a large-area degradation")
	require.Equal(t, "10", rdb.HGet(ctx, statsKey, "exit_window_sample_count").Val())
	require.Equal(t, "6", rdb.HGet(ctx, statsKey, "exit_window_slow_count").Val())

	for index := 6; index < 9; index++ {
		require.NoError(t, cache.RecordSample(ctx, accountID, fmt.Sprintf("recent-slow-%d", index), 20_000))
	}
	stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast, "nine of thirteen requests is still below seventy percent")

	require.NoError(t, cache.RecordSample(ctx, accountID, "recent-slow-9", 20_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast, "ten of fourteen slow requests must evict the fast account")
	require.Equal(t, "14", rdb.HGet(ctx, statsKey, "exit_window_sample_count").Val())
	require.Equal(t, "10", rdb.HGet(ctx, statsKey, "exit_window_slow_count").Val())
	require.Equal(t, "1", rdb.HGet(ctx, statsKey, "exit_window_degraded").Val())
}

func TestTotalLatencyStatsCacheFastExitRequiresAbruptDifferenceFromBaseline(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(27)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	base := time.Unix(1_700_150_000, 0)
	recordSustainedFastTotalLatencySamples(t, mr, cache, accountID, "borderline-fast", base)
	members, err := rdb.ZRange(ctx, fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID), 0, -1).Result()
	require.NoError(t, err)
	for _, member := range members {
		updated := strings.TrimSuffix(member, ":8000") + ":12000"
		score, scoreErr := rdb.ZScore(ctx, fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID), member).Result()
		require.NoError(t, scoreErr)
		require.NoError(t, rdb.ZRem(ctx, fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID), member).Err())
		require.NoError(t, rdb.ZAdd(ctx, fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID), redis.Z{Score: score, Member: updated}).Err())
	}
	require.NoError(t, rdb.HSet(ctx, statsKey, "normal_total_ms", "12000", "p50_ms", "12000", "p90_ms", "12000").Err())

	for index := 0; index < 10; index++ {
		mr.SetTime(base.Add(4*time.Minute + time.Duration(index)*15*time.Second))
		require.NoError(t, cache.RecordSample(ctx, accountID, fmt.Sprintf("moderately-slow-%d", index), 16_000))
	}

	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast, "a four-second shift from a borderline baseline is not an abrupt large difference")
	require.Equal(t, "10", rdb.HGet(ctx, statsKey, "exit_window_slow_count").Val())
	require.Equal(t, "0", rdb.HGet(ctx, statsKey, "exit_window_degraded").Val())
	require.Equal(t, "12000", rdb.HGet(ctx, statsKey, "baseline_total_ms").Val())
	require.Equal(t, "16000", rdb.HGet(ctx, statsKey, "recent_total_ms").Val())
}

func TestTotalLatencyStatsCacheFirstFastSampleCreatesRecoveryCandidate(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(25)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	base := time.Unix(1_700_200_000, 0)
	mr.SetTime(base)
	recordTotalLatencySamples(t, cache, accountID, "slow", repeatedDurations(22, 30_000))

	mr.SetTime(base.Add(4 * time.Minute))
	recordTotalLatencySamples(t, cache, accountID, "recent-slow", repeatedDurations(3, 30_000))
	mr.SetTime(base.Add(4*time.Minute + 30*time.Second))
	require.NoError(t, cache.RecordSample(ctx, accountID, "first-fast", 9_000))
	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast)
	require.Equal(t, 1, stats[accountID].RecoveryFastStreak)
	require.Equal(t, "4", rdb.HGet(ctx, statsKey, "exit_window_sample_count").Val())
	require.Equal(t, "1", rdb.HGet(ctx, statsKey, "exit_window_fast_count").Val())
	require.Equal(t, "1", rdb.HGet(ctx, statsKey, "recovery_candidate").Val())
	require.Equal(t, "0", rdb.HGet(ctx, statsKey, "recovery_window_confirmed").Val())

	mr.SetTime(base.Add(5 * time.Minute))
	require.NoError(t, cache.RecordSample(ctx, accountID, "candidate-noise", 30_000))
	stats, err = cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, stats[accountID].ReliableFast)
	require.Zero(t, stats[accountID].RecoveryFastStreak, "a failed follow-up probe must remove an isolated fast request from recovery")
}

func TestTotalLatencyStatsCacheRecoveryCandidateGetsEnoughProofToReenterFastPool(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	accountID := int64(26)
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	base := time.Unix(1_700_300_000, 0)
	mr.SetTime(base)
	recordTotalLatencySamples(t, cache, accountID, "slow", repeatedDurations(22, 30_000))

	for index, sample := range []struct {
		after    time.Duration
		duration int
	}{
		{0, 8_000},
		{30 * time.Second, 9_000},
		{60 * time.Second, 10_000},
		{90 * time.Second, 30_000},
	} {
		mr.SetTime(base.Add(4*time.Minute + sample.after))
		require.NoError(t, cache.RecordSample(ctx, accountID, fmt.Sprintf("recovery-%d", index), sample.duration))
	}

	stats, err := cache.GetStatsBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, stats[accountID].ReliableFast, "three of four fast samples across ninety seconds confirm recovery")
	require.Zero(t, stats[accountID].RecoveryFastStreak)
	require.Equal(t, 9_500.0, stats[accountID].PredictedMS)
	require.Equal(t, "3", rdb.HGet(ctx, statsKey, "exit_window_fast_count").Val())
	require.Equal(t, "1", rdb.HGet(ctx, statsKey, "recovery_window_confirmed").Val())
}

func TestTotalLatencyStatsCacheUsesRetainedSamplesWithinTwentyFourHours(t *testing.T) {
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
	base := time.Unix(1_700_400_000, 0)
	recordSustainedFastTotalLatencySamples(t, mr, cache, 20, "fast", base)
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
