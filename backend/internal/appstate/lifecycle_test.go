package appstate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitForZeroTracksActiveRequest(t *testing.T) {
	require.True(t, TryBeginRequest())
	require.Equal(t, int64(1), ActiveRequests())
	go func() {
		time.Sleep(20 * time.Millisecond)
		EndRequest()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, WaitForZero(ctx))
	require.Zero(t, ActiveRequests())
}

func TestBeginDrainRejectsNewRequests(t *testing.T) {
	draining.Store(false)
	activeRequests.Store(0)
	t.Cleanup(func() {
		draining.Store(false)
		activeRequests.Store(0)
	})

	BeginDrain()
	require.False(t, TryBeginRequest())
	require.Zero(t, ActiveRequests())
}
