package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestFirstTokenLatencyCounterCache_TracksRatioDeduplicatesAndHonorsPauseClaim(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyCounterCache{rdb: rdb}
	ctx := context.Background()

	counts, err := cache.RecordFirstTokenSample(ctx, 42, "rule-a", 60, "request-1", true)
	require.NoError(t, err)
	require.Equal(t, service.FirstTokenLatencySampleCounts{Total: 1, Slow: 1}, counts)
	counts, err = cache.RecordFirstTokenSample(ctx, 42, "rule-a", 60, "request-1", true)
	require.NoError(t, err)
	require.Equal(t, service.FirstTokenLatencySampleCounts{Total: 1, Slow: 1}, counts)
	counts, err = cache.RecordFirstTokenSample(ctx, 42, "rule-a", 60, "request-2", false)
	require.NoError(t, err)
	require.Equal(t, service.FirstTokenLatencySampleCounts{Total: 2, Slow: 1}, counts)
	counts, err = cache.RecordFirstTokenSample(ctx, 42, "rule-b", 300, "request-1", true)
	require.NoError(t, err)
	require.Equal(t, service.FirstTokenLatencySampleCounts{Total: 1, Slow: 1}, counts)

	claimed, err := cache.ClaimFirstTokenPause(ctx, 42, 600)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimFirstTokenPause(ctx, 42, 600)
	require.NoError(t, err)
	require.False(t, claimed)
	counts, err = cache.RecordFirstTokenSample(ctx, 42, "rule-a", 60, "request-3", true)
	require.NoError(t, err)
	require.Equal(t, service.FirstTokenLatencySampleCounts{Total: -1, Slow: -1}, counts)

	require.NoError(t, cache.ResetFirstTokenSamples(ctx, 42))
	require.NoError(t, cache.ReleaseFirstTokenPauseClaim(ctx, 42))
	counts, err = cache.RecordFirstTokenSample(ctx, 42, "rule-a", 60, "request-4", false)
	require.NoError(t, err)
	require.Equal(t, service.FirstTokenLatencySampleCounts{Total: 1, Slow: 0}, counts)
}

func TestFirstTokenLatencyCounterCache_WindowExpiration(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyCounterCache{rdb: rdb}
	ctx := context.Background()

	_, err := cache.RecordFirstTokenSample(ctx, 7, "short", 1, "request-1", true)
	require.NoError(t, err)
	totalKey := firstTokenLatencyCounterPrefix + "7:rule:short:total"
	slowKey := firstTokenLatencyCounterPrefix + "7:rule:short:slow"
	oldScore := float64(time.Now().Add(-2 * time.Second).UnixMilli())
	require.NoError(t, rdb.ZAddXX(ctx, totalKey, redis.Z{
		Score:  oldScore,
		Member: "request-1",
	}).Err())
	require.NoError(t, rdb.ZAddXX(ctx, slowKey, redis.Z{
		Score:  oldScore,
		Member: "request-1",
	}).Err())
	counts, err := cache.RecordFirstTokenSample(ctx, 7, "short", 1, "request-2", false)
	require.NoError(t, err)
	require.Equal(t, service.FirstTokenLatencySampleCounts{Total: 1, Slow: 0}, counts)
}
