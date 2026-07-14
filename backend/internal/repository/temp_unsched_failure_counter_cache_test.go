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

func TestTempUnschedFailureCounterCache_MultipleRulesDeduplicateAndReset(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &tempUnschedFailureCounterCache{rdb: rdb}
	ctx := context.Background()

	count, err := cache.RecordFailure(ctx, 42, "rule-a", 60, "request-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = cache.RecordFailure(ctx, 42, "rule-a", 60, "request-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = cache.RecordFailure(ctx, 42, "rule-b", 300, "request-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = cache.RecordFailure(ctx, 42, "rule-a", 60, "request-2")
	require.NoError(t, err)
	require.Equal(t, int64(2), count)

	require.NoError(t, cache.ResetFailures(ctx, 42))
	count, err = cache.RecordFailure(ctx, 42, "rule-a", 60, "request-3")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}

func TestTempUnschedFailureCounterCache_WindowExpiration(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &tempUnschedFailureCounterCache{rdb: rdb}
	ctx := context.Background()

	count, err := cache.RecordFailure(ctx, 9, "short", 1, "request-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	counterKey := fmt.Sprintf("%s%d:rule:%s", tempUnschedFailureCounterPrefix, 9, "short")
	require.NoError(t, rdb.ZAddXX(ctx, counterKey, redis.Z{
		Score:  float64(time.Now().Add(-2 * time.Second).UnixMilli()),
		Member: "request-1",
	}).Err())
	count, err = cache.RecordFailure(ctx, 9, "short", 1, "request-2")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}
