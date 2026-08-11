//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type firstTokenLatencyCounterStub struct {
	counts       map[string]int64
	recordedKeys []string
	claimed      bool
	claimSeconds int
	released     bool
	reset        bool
}

func (s *firstTokenLatencyCounterStub) RecordSlowFirstToken(_ context.Context, _ int64, ruleKey string, _ int, _ string) (int64, error) {
	s.recordedKeys = append(s.recordedKeys, ruleKey)
	s.counts[ruleKey]++
	return s.counts[ruleKey], nil
}

func (s *firstTokenLatencyCounterStub) ClaimFirstTokenPause(_ context.Context, _ int64, pauseSeconds int) (bool, error) {
	s.claimSeconds = pauseSeconds
	if s.claimed {
		return false, nil
	}
	s.claimed = true
	return true, nil
}

func (s *firstTokenLatencyCounterStub) ReleaseFirstTokenPauseClaim(_ context.Context, _ int64) error {
	s.released = true
	return nil
}

func (s *firstTokenLatencyCounterStub) ResetSlowFirstTokens(_ context.Context, _ int64) error {
	s.reset = true
	return nil
}

func newFirstTokenLatencyRateLimitService(t *testing.T, rules []FirstTokenLatencyAutoPauseRule) (*RateLimitService, *rateLimitAccountRepoStub, *firstTokenLatencyCounterStub) {
	t.Helper()
	settingsRepo := newMockSettingRepo()
	settingsJSON, err := json.Marshal(FirstTokenLatencyAutoPauseSettings{Enabled: true, Rules: rules})
	require.NoError(t, err)
	settingsRepo.data[SettingKeyFirstTokenLatencyAutoPauseSettings] = string(settingsJSON)

	accountRepo := &rateLimitAccountRepoStub{}
	counter := &firstTokenLatencyCounterStub{counts: map[string]int64{}}
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(NewSettingService(settingsRepo, &config.Config{}))
	svc.SetFirstTokenLatencyCounterCache(counter)
	return svc, accountRepo, counter
}

func TestObserveFirstTokenLatency_AnyRuleCanTrigger(t *testing.T) {
	rules := []FirstTokenLatencyAutoPauseRule{
		{WindowMinutes: 5, ThresholdSeconds: 5, TriggerCount: 3, PauseMinutes: 5},
		{WindowMinutes: 1, ThresholdSeconds: 10, TriggerCount: 1, PauseMinutes: 20},
	}
	svc, repo, counter := newFirstTokenLatencyRateLimitService(t, rules)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	firstTokenMS := 11_000

	blocked := svc.ObserveFirstTokenLatency(context.Background(), account, "request-1", &firstTokenMS)

	require.True(t, blocked)
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, 20*60, counter.claimSeconds)
	require.True(t, counter.reset)
	require.NotNil(t, account.TempUnschedulableUntil)
	require.WithinDuration(t, time.Now().Add(20*time.Minute), *account.TempUnschedulableUntil, 2*time.Second)
}

func TestObserveFirstTokenLatency_MultipleMatchesUseLongestPause(t *testing.T) {
	rules := []FirstTokenLatencyAutoPauseRule{
		{WindowMinutes: 5, ThresholdSeconds: 5, TriggerCount: 1, PauseMinutes: 5},
		{WindowMinutes: 1, ThresholdSeconds: 10, TriggerCount: 1, PauseMinutes: 20},
	}
	svc, repo, counter := newFirstTokenLatencyRateLimitService(t, rules)
	account := &Account{ID: 43, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}
	firstTokenMS := 11_000

	blocked := svc.ObserveFirstTokenLatency(context.Background(), account, "request-2", &firstTokenMS)

	require.True(t, blocked)
	require.Len(t, counter.recordedKeys, 2)
	require.Equal(t, 20*60, counter.claimSeconds)
	require.Contains(t, repo.lastTempReason, `"rule_index":1`)
	require.Contains(t, repo.lastTempReason, `"pause_minutes":20`)
}

func TestObserveFirstTokenLatency_ExactThresholdDoesNotCount(t *testing.T) {
	rules := []FirstTokenLatencyAutoPauseRule{
		{WindowMinutes: 5, ThresholdSeconds: 10, TriggerCount: 1, PauseMinutes: 5},
	}
	svc, repo, counter := newFirstTokenLatencyRateLimitService(t, rules)
	account := &Account{ID: 44, Platform: PlatformGrok, Status: StatusActive, Schedulable: true}
	firstTokenMS := 10_000

	blocked := svc.ObserveFirstTokenLatency(context.Background(), account, "request-3", &firstTokenMS)

	require.False(t, blocked)
	require.Empty(t, counter.recordedKeys)
	require.Zero(t, repo.tempCalls)
}
