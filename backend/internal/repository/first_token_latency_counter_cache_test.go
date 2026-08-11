package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestFirstTokenLatencyCounterCache_DeduplicatesRulesAndHonorsPauseClaim(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyCounterCache{rdb: rdb}
	ctx := context.Background()

	count, err := cache.RecordSlowFirstToken(ctx, 42, "rule-a", 60, "request-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = cache.RecordSlowFirstToken(ctx, 42, "rule-a", 60, "request-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = cache.RecordSlowFirstToken(ctx, 42, "rule-b", 300, "request-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	claimed, err := cache.ClaimFirstTokenPause(ctx, 42, 600)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimFirstTokenPause(ctx, 42, 600)
	require.NoError(t, err)
	require.False(t, claimed)
	count, err = cache.RecordSlowFirstToken(ctx, 42, "rule-a", 60, "request-2")
	require.NoError(t, err)
	require.Equal(t, int64(-1), count)

	require.NoError(t, cache.ResetSlowFirstTokens(ctx, 42))
	require.NoError(t, cache.ReleaseFirstTokenPauseClaim(ctx, 42))
	count, err = cache.RecordSlowFirstToken(ctx, 42, "rule-a", 60, "request-3")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}

func TestFirstTokenLatencyCounterCache_WindowExpiration(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &firstTokenLatencyCounterCache{rdb: rdb}
	ctx := context.Background()

	_, err := cache.RecordSlowFirstToken(ctx, 7, "short", 1, "request-1")
	require.NoError(t, err)
	counterKey := firstTokenLatencyCounterPrefix + "7:rule:short"
	require.NoError(t, rdb.ZAddXX(ctx, counterKey, redis.Z{
		Score:  float64(time.Now().Add(-2 * time.Second).UnixMilli()),
		Member: "request-1",
	}).Err())
	count, err := cache.RecordSlowFirstToken(ctx, 7, "short", 1, "request-2")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}
