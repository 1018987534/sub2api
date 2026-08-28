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

func TestLotteryCaptchaStoreIsSharedTTLAndSingleUse(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewLotteryCaptchaStore(client)
	record := service.LotteryCaptchaRecord{
		UserID: 42, ClientIP: "203.0.113.42", TargetX: 150, TargetY: 80,
	}

	require.NoError(t, store.Save(context.Background(), "challenge", record, 2*time.Minute))
	require.Equal(t, 2*time.Minute, server.TTL(lotteryCaptchaKeyPrefix+"challenge"))
	got, err := store.Take(context.Background(), "challenge")
	require.NoError(t, err)
	require.Equal(t, record, got)
	_, err = store.Take(context.Background(), "challenge")
	require.ErrorIs(t, err, service.ErrLotteryCaptchaRecordNotFound)
}
