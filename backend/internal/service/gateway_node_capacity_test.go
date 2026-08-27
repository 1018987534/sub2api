package service

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayNodeCapacityStoreEnforcesHardLimitAndReleases(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := NewGatewayNodeCapacityStore(rdb)

	first, current, err := store.Acquire(context.Background(), "node-a", 2)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 1, current)
	t.Cleanup(first.Release)

	second, current, err := store.Acquire(context.Background(), "node-a", 2)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, 2, current)
	t.Cleanup(second.Release)

	blocked, current, err := store.Acquire(context.Background(), "node-a", 2)
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, 2, current)

	first.Release()
	replacement, current, err := store.Acquire(context.Background(), "node-a", 2)
	require.NoError(t, err)
	require.NotNil(t, replacement)
	require.Equal(t, 2, current)
	replacement.Release()

	remaining, err := store.Current(context.Background(), "node-a")
	require.NoError(t, err)
	require.Equal(t, 1, remaining)
}

func TestGatewayNodeCapacityStoreSeparatesNodes(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := NewGatewayNodeCapacityStore(rdb)

	first, _, err := store.Acquire(context.Background(), "node-a", 1)
	require.NoError(t, err)
	require.NotNil(t, first)
	t.Cleanup(first.Release)

	other, current, err := store.Acquire(context.Background(), "node-b", 1)
	require.NoError(t, err)
	require.NotNil(t, other)
	require.Equal(t, 1, current)
	other.Release()
}
