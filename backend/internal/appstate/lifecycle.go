package appstate

import (
	"context"
	"sync/atomic"
	"time"
)

var draining atomic.Bool
var activeRequests atomic.Int64

func BeginDrain() {
	draining.Store(true)
}

func IsDraining() bool {
	return draining.Load()
}

// TryBeginRequest atomically joins the active request set unless draining has
// begun. The second check closes the race where draining starts immediately
// after the first check.
func TryBeginRequest() bool {
	if IsDraining() {
		return false
	}
	activeRequests.Add(1)
	if IsDraining() {
		activeRequests.Add(-1)
		return false
	}
	return true
}

func EndRequest() {
	activeRequests.Add(-1)
}

func ActiveRequests() int64 {
	return activeRequests.Load()
}

func WaitForZero(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ActiveRequests() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
