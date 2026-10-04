package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntelligencePoolReset(t *testing.T) {
	mr, rdb, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	recordTotalLatencySamples(t, cache, 7, "fast", repeatedDurations(24, 8000))
	stats, err := cache.GetStatsBatch(ctx, []int64{7})
	require.NoError(t, err)
	require.True(t, stats[7].ReliableFast)
	require.NoError(t, cache.RequestManualProbe(ctx, 7, time.Hour))
	until := time.Now().Add(20 * time.Minute)
	require.NoError(t, cache.ResetForIntelligencePause(ctx, 7, until))
	for i := 0; i < 2; i++ {
		stats, err = cache.GetStatsBatch(ctx, []int64{7})
		require.NoError(t, err)
		require.Zero(t, stats[7].SampleCount)
		require.Zero(t, stats[7].PredictedMS)
		require.True(t, stats[7].CircuitBroken)
		require.False(t, stats[7].ReliableFast)
		require.NoError(t, cache.RecordSample(ctx, 7, "late-in-flight", 8000))
	}
	require.False(t, mr.Exists(totalLatencySamplesPrefix+"7"))
	ids, err := cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	// Move Redis's clock across the pause boundary without waiting 20 minutes.
	mr.SetTime(until.Add(time.Second))
	require.NoError(t, cache.RecordSample(ctx, 7, "new-after-cooldown", 8000))
	stats, err = cache.GetStatsBatch(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats[7].SampleCount)
	require.False(t, stats[7].ReliableFast)
	require.Equal(t, int64(1), rdb.ZCard(ctx, totalLatencySamplesPrefix+"7").Val())
	t.Log("fast -> pending; late sample ignored; +20m -> one fresh sample, still pending")
}

func TestTotalLatencyThirtyThirtyThreeStaircase(t *testing.T) {
	for _, initialFast := range []bool{false, true} {
		_, rdb, cache := newTotalLatencyTestCache(t)
		ctx := context.Background()
		for _, duration := range []int{30_000, 30_001, 33_000, 33_001} {
			id := int64(duration)
			recordTotalLatencySamples(t, cache, id, "seed", repeatedDurations(19, duration))
			if initialFast {
				require.NoError(t, rdb.HSet(ctx, fmt.Sprintf("%s%d", totalLatencyStatsPrefix, id), "is_fast", 1).Err())
			}
			for n := 0; n < 3; n++ {
				require.NoError(t, cache.RecordSample(ctx, id, fmt.Sprintf("confirmation-%d", n), duration))
				stats, err := cache.GetStatsBatch(ctx, []int64{id})
				require.NoError(t, err)
				want := initialFast
				if n == 2 && duration <= 30_000 {
					want = true
				}
				if n == 2 && duration > 33_000 {
					want = false
				}
				require.Equal(t, want, stats[id].ReliableFast, "initial=%v duration=%d confirmation=%d", initialFast, duration, n+1)
			}
		}
	}
}

func TestIntelligenceIsolationSchedulerProjection(t *testing.T) {
	extra := map[string]any{"intelligence_pause_until": "2030-01-01T00:00:00Z", "intelligence_allowed_groups": []int64{111}, "intelligence_recovery_required": true}
	require.Equal(t, extra, filterSchedulerExtra(extra))
	require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(extra))
}

func TestIntelligenceNormalResultResumesSamplesImmediately(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.ResetForIntelligencePause(ctx, 7, time.Now().Add(20*time.Minute)))
	require.NoError(t, cache.ClearIntelligencePause(ctx, 7))
	require.NoError(t, cache.RecordSample(ctx, 7, "after-normal", 1000))
	stats, err := cache.GetStatsBatch(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats[7].SampleCount)
	require.False(t, stats[7].ReliableFast)
}

func TestIntelligenceLegacyClearMatchesReasonAndOutbox(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(0)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)
	require.NoError(t, repo.ClearIntelligenceTempUnschedulable(context.Background(), 7, "intelligence: old pause"))
	require.Len(t, exec.execQueries, 1)
	require.Contains(t, exec.execQueries[0], "temp_unschedulable_reason=$2")
	require.Contains(t, exec.execQueries[0], "INSERT INTO scheduler_outbox")
	require.NoError(t, repo.ClearIntelligenceTempUnschedulable(context.Background(), 7, "unrelated"))
	require.Len(t, exec.execQueries, 1)
}
