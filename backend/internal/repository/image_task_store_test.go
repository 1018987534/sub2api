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

func TestImageTaskStoreRoundTripAndTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := NewImageTaskStore(rdb)
	task := &service.ImageTaskRecord{
		ID:        "imgtask_123",
		UserID:    7,
		APIKeyID:  9,
		Status:    service.ImageTaskStatusProcessing,
		CreatedAt: 100,
		ExpiresAt: 200,
	}

	require.NoError(t, store.Save(context.Background(), task, 24*time.Hour))
	got, err := store.Get(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, task, got)
	require.Equal(t, 24*time.Hour, mr.TTL(imageTaskKey(task.ID)))
	require.Equal(t, 24*time.Hour, mr.TTL(imageTaskIndexKey(task.APIKeyID)))
	members, err := mr.ZMembers(imageTaskIndexKey(task.APIKeyID))
	require.NoError(t, err)
	require.Equal(t, []string{task.ID}, members)
}

func TestImageTaskStoreMissing(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := NewImageTaskStore(rdb)

	_, err := store.Get(context.Background(), "imgtask_missing")
	require.ErrorIs(t, err, service.ErrImageTaskNotFound)
}

func TestImageTaskStoreListsNewestTasksAndDeletesRecords(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := NewImageTaskStore(rdb)
	owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
	older := &service.ImageTaskRecord{ID: "imgtask_old", UserID: owner.UserID, APIKeyID: owner.APIKeyID, Status: service.ImageTaskStatusCompleted, CreatedAt: 100}
	newer := &service.ImageTaskRecord{ID: "imgtask_new", UserID: owner.UserID, APIKeyID: owner.APIKeyID, Status: service.ImageTaskStatusCompleted, CreatedAt: 200}
	require.NoError(t, store.Save(context.Background(), older, time.Hour))
	require.NoError(t, store.Save(context.Background(), newer, time.Hour))
	require.NoError(t, rdb.ZAdd(context.Background(), imageTaskIndexKey(owner.APIKeyID), redis.Z{Score: 300, Member: "imgtask_expired"}).Err())

	listed, err := store.List(context.Background(), owner, 10)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, newer.ID, listed[0].ID)
	require.Equal(t, older.ID, listed[1].ID)
	require.Equal(t, redis.Nil, rdb.ZScore(context.Background(), imageTaskIndexKey(owner.APIKeyID), "imgtask_expired").Err())

	otherOwner, err := store.List(context.Background(), service.ImageTaskOwner{UserID: 8, APIKeyID: owner.APIKeyID}, 10)
	require.NoError(t, err)
	require.Empty(t, otherOwner)

	require.NoError(t, store.Delete(context.Background(), newer))
	_, err = store.Get(context.Background(), newer.ID)
	require.ErrorIs(t, err, service.ErrImageTaskNotFound)
	require.Equal(t, redis.Nil, rdb.ZScore(context.Background(), imageTaskIndexKey(owner.APIKeyID), newer.ID).Err())
}
