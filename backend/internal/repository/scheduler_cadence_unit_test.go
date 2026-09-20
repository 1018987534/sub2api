//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSchedulerCacheFullRebuildCadencePersistsAcrossInstances(t *testing.T) {
	cache, redisServer := newSchedulerCacheUnitWithRedis(t)
	ctx := context.Background()
	got, err := cache.GetFullRebuildCompletedAt(ctx)
	require.NoError(t, err)
	require.True(t, got.IsZero())
	at := time.Now().UTC()
	require.NoError(t, cache.SetFullRebuildCompletedAt(ctx, at))
	peer := &schedulerCache{rdb: cache.rdb}
	got, err = peer.GetFullRebuildCompletedAt(ctx)
	require.NoError(t, err)
	require.True(t, at.Equal(got))
	require.Zero(t, redisServer.TTL(schedulerFullRebuildCompletedAtKey))
	require.NoError(t, redisServer.Set(schedulerFullRebuildCompletedAtKey, "invalid"))
	_, err = peer.GetFullRebuildCompletedAt(ctx)
	require.Error(t, err)
}

func TestSchedulerLeaderLeaseOwnerAndCrashExpiry(t *testing.T) {
	cache, server := newSchedulerCacheUnitWithRedis(t)
	lock := NewLeaderLockCache(cache.rdb)
	ctx := context.Background()
	acquired, err := lock.TryAcquireLeaderLock(ctx, "scheduler-test", "a", 3*time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = lock.TryAcquireLeaderLock(ctx, "scheduler-test", "b", 3*time.Minute)
	require.NoError(t, err)
	require.False(t, acquired)
	server.FastForward(3 * time.Minute)
	acquired, err = lock.TryAcquireLeaderLock(ctx, "scheduler-test", "b", 3*time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, lock.ReleaseLeaderLock(ctx, "scheduler-test", "a"))
	owner, err := server.Get(leaderLockKeyPrefix + "scheduler-test")
	require.NoError(t, err)
	require.Equal(t, "b", owner)
	require.NoError(t, lock.ReleaseLeaderLock(ctx, "scheduler-test", "b"))
	require.False(t, server.Exists(leaderLockKeyPrefix+"scheduler-test"))
}
