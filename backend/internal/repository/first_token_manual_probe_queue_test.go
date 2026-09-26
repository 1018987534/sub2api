package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTotalLatencyManualProbePendingQueue(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	base := time.Now()
	mr.SetTime(base)
	require.NoError(t, cache.RequestManualProbe(ctx, 23, 10*time.Minute))
	mr.SetTime(base.Add(time.Second))
	require.NoError(t, cache.RequestManualProbe(ctx, 22, 10*time.Minute))
	require.NoError(t, cache.RequestManualProbe(ctx, 23, 10*time.Minute))
	ids, err := cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{23, 22}, ids, "repeated clicks coalesce without starving an older request")
	ids, err = cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{23, 22}, ids, "inspection must not consume queued work")
	claimedID, claimed, err := cache.TryClaimManualProbe(ctx, []int64{23}, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, int64(23), claimedID)
	ids, err = cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{22}, ids)
	mr.FastForward(11 * time.Minute)
	ids, err = cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestTotalLatencyManualProbeExpiresIndependently(t *testing.T) {
	mr, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.RequestManualProbe(ctx, 1, time.Second))
	require.NoError(t, cache.RequestManualProbe(ctx, 2, time.Minute))
	mr.FastForward(2 * time.Second)
	ids, err := cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, ids)
	require.NoError(t, cache.RequestManualProbe(ctx, 3, time.Second))
	mr.FastForward(2 * time.Second)
	ids, err = cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, ids, "a shorter request must not shorten another account's queue TTL")
}

func TestTotalLatencyManualProbeSurvivesInflightSample(t *testing.T) {
	for _, duration := range []int{5000, 600000} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			_, _, cache := newTotalLatencyTestCache(t)
			ctx := context.Background()
			require.NoError(t, cache.RequestManualProbe(ctx, 7, time.Minute))
			for i := 0; i < 3; i++ {
				require.NoError(t, cache.RecordSample(ctx, 7, fmt.Sprintf("inflight-%d", i), duration))
			}
			ids, err := cache.PendingManualProbeAccountIDs(ctx)
			require.NoError(t, err)
			require.Equal(t, []int64{7}, ids, "completion or circuit reset of old traffic must not cancel the admin request")
			_, claimed, err := cache.TryClaimManualProbe(ctx, []int64{7}, time.Minute)
			require.NoError(t, err)
			require.True(t, claimed)
		})
	}
}

func TestTotalLatencyManualProbeConcurrentGatewaysClaimOnce(t *testing.T) {
	_, _, cache := newTotalLatencyTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.RequestManualProbe(ctx, 9, time.Minute))
	type result struct {
		id      int64
		claimed bool
		err     error
	}
	results := make(chan result, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, claimed, err := cache.TryClaimManualProbe(ctx, []int64{9}, time.Minute)
			results <- result{id, claimed, err}
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.claimed {
			winners++
			require.Equal(t, int64(9), result.id)
		}
	}
	require.Equal(t, 1, winners)
	ids, err := cache.PendingManualProbeAccountIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
}
