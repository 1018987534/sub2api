//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type consecutiveFailureCounterStub struct {
	counts map[string]int64
	events map[string]map[string]struct{}
	resets []int64
}

func newConsecutiveFailureCounterStub() *consecutiveFailureCounterStub {
	return &consecutiveFailureCounterStub{
		counts: make(map[string]int64),
		events: make(map[string]map[string]struct{}),
	}
}

func (c *consecutiveFailureCounterStub) RecordFailure(_ context.Context, _ int64, ruleKey string, _ int, eventID string) (int64, error) {
	if c.events[ruleKey] == nil {
		c.events[ruleKey] = make(map[string]struct{})
	}
	if _, exists := c.events[ruleKey][eventID]; exists {
		return c.counts[ruleKey], nil
	}
	c.events[ruleKey][eventID] = struct{}{}
	c.counts[ruleKey]++
	return c.counts[ruleKey], nil
}

func (c *consecutiveFailureCounterStub) ResetFailures(_ context.Context, accountID int64) error {
	c.resets = append(c.resets, accountID)
	c.counts = make(map[string]int64)
	c.events = make(map[string]map[string]struct{})
	return nil
}

type consecutiveFailureRepoStub struct {
	mockAccountRepoForGemini
	setCalls int
	until    time.Time
	reason   string
}

func (r *consecutiveFailureRepoStub) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, reason string) error {
	r.setCalls++
	r.until = until
	r.reason = reason
	return nil
}

type consecutiveFailureStateCache struct {
	state *TempUnschedState
}

func (c *consecutiveFailureStateCache) SetTempUnsched(_ context.Context, _ int64, state *TempUnschedState) error {
	clone := *state
	c.state = &clone
	return nil
}

func (c *consecutiveFailureStateCache) GetTempUnsched(_ context.Context, _ int64) (*TempUnschedState, error) {
	return c.state, nil
}

func (c *consecutiveFailureStateCache) DeleteTempUnsched(_ context.Context, _ int64) error {
	c.state = nil
	return nil
}

func consecutiveFailureAccount() *Account {
	return &Account{
		ID:          77,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_mode":    TempUnschedulableModeConsecutiveFailures,
			"temp_unschedulable_failure_rules": []any{
				map[string]any{
					"window_seconds":    float64(60),
					"failure_threshold": float64(2),
					"duration_minutes":  float64(10),
				},
				map[string]any{
					"window_seconds":    float64(300),
					"failure_threshold": float64(2),
					"duration_minutes":  float64(30),
				},
			},
		},
	}
}

func TestRateLimitService_ConsecutiveFailuresAllRulesAndLongestPause(t *testing.T) {
	repo := &consecutiveFailureRepoStub{}
	counter := newConsecutiveFailureCounterStub()
	stateCache := &consecutiveFailureStateCache{}
	svc := NewRateLimitService(repo, nil, nil, nil, stateCache)
	svc.SetTempUnschedFailureCounterCache(counter)
	account := consecutiveFailureAccount()

	ctx1 := context.WithValue(context.Background(), ctxkey.ClientRequestID, "failure-1")
	require.False(t, svc.tryConsecutiveFailureTempUnschedulable(ctx1, account, http.StatusBadGateway, []byte("first")))
	require.Len(t, counter.counts, 2)

	ctx2 := context.WithValue(context.Background(), ctxkey.ClientRequestID, "failure-2")
	require.True(t, svc.tryConsecutiveFailureTempUnschedulable(ctx2, account, http.StatusServiceUnavailable, []byte("second")))
	require.Equal(t, 1, repo.setCalls)
	require.WithinDuration(t, time.Now().Add(30*time.Minute), repo.until, 3*time.Second)
	require.NotNil(t, stateCache.state)
	require.Equal(t, 1, stateCache.state.RuleIndex)
	require.Equal(t, int64(2), stateCache.state.FailureCount)
	require.Equal(t, 2, stateCache.state.FailureThreshold)
	require.Equal(t, 300, stateCache.state.WindowSeconds)
	require.Equal(t, TempUnschedulableModeConsecutiveFailures, stateCache.state.TriggerMode)
}

func TestRateLimitService_ConsecutiveFailureDuplicateRequestCountsOnce(t *testing.T) {
	repo := &consecutiveFailureRepoStub{}
	counter := newConsecutiveFailureCounterStub()
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	svc.SetTempUnschedFailureCounterCache(counter)
	account := consecutiveFailureAccount()
	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "same-request")

	require.False(t, svc.tryConsecutiveFailureTempUnschedulable(ctx, account, 500, []byte("failed")))
	require.False(t, svc.tryConsecutiveFailureTempUnschedulable(ctx, account, 500, []byte("failed again")))
	for _, count := range counter.counts {
		require.Equal(t, int64(1), count)
	}
	require.Zero(t, repo.setCalls)
}

func TestRateLimitService_ConsecutiveFailureSuccessAndCanceledTransport(t *testing.T) {
	repo := &consecutiveFailureRepoStub{}
	counter := newConsecutiveFailureCounterStub()
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	svc.SetTempUnschedFailureCounterCache(counter)
	account := consecutiveFailureAccount()

	require.False(t, svc.HandleTempUnschedulableTransportFailure(context.Background(), account, context.Canceled))
	require.Empty(t, counter.counts)
	require.False(t, svc.HandleTempUnschedulableTransportFailure(context.Background(), account, errors.New("dial tcp timeout")))
	require.Len(t, counter.counts, 2)

	svc.ResetTempUnschedulableFailureCounters(context.Background(), account.ID)
	require.Equal(t, []int64{account.ID}, counter.resets)
	require.Empty(t, counter.counts)
}

func TestRateLimitService_ConsecutiveFailurePrecedesCustomErrorCodeFilter(t *testing.T) {
	repo := &consecutiveFailureRepoStub{}
	counter := newConsecutiveFailureCounterStub()
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	svc.SetTempUnschedFailureCounterCache(counter)
	account := consecutiveFailureAccount()
	account.Credentials["temp_unschedulable_failure_rules"] = []any{map[string]any{
		"window_seconds":    float64(60),
		"failure_threshold": float64(1),
		"duration_minutes":  float64(5),
	}}
	account.Credentials["custom_error_codes_enabled"] = true
	account.Credentials["custom_error_codes"] = []any{float64(http.StatusTooManyRequests)}

	handled := svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, []byte("bad gateway"))
	require.True(t, handled)
	require.Equal(t, 1, repo.setCalls)
}
