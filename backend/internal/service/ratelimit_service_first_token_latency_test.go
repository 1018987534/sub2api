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
	counts       map[string]FirstTokenLatencySampleCounts
	recordedKeys []string
	claimed      bool
	claimSeconds int
	released     bool
	reset        bool
}

func (s *firstTokenLatencyCounterStub) RecordFirstTokenSample(_ context.Context, _ int64, ruleKey string, _ int, _ string, slow bool) (FirstTokenLatencySampleCounts, error) {
	s.recordedKeys = append(s.recordedKeys, ruleKey)
	counts := s.counts[ruleKey]
	counts.Total++
	if slow {
		counts.Slow++
	}
	s.counts[ruleKey] = counts
	return counts, nil
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

func (s *firstTokenLatencyCounterStub) ResetFirstTokenSamples(_ context.Context, _ int64) error {
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
	counter := &firstTokenLatencyCounterStub{counts: map[string]FirstTokenLatencySampleCounts{}}
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(NewSettingService(settingsRepo, &config.Config{}))
	svc.SetFirstTokenLatencyCounterCache(counter)
	return svc, accountRepo, counter
}

func TestObserveFirstTokenLatency_AnyRuleCanTrigger(t *testing.T) {
	rules := []FirstTokenLatencyAutoPauseRule{
		{WindowMinutes: 5, ThresholdSeconds: 5, TriggerPercent: 75, PauseMinutes: 5},
		{WindowMinutes: 1, ThresholdSeconds: 10, TriggerPercent: 50, PauseMinutes: 20},
	}
	svc, repo, counter := newFirstTokenLatencyRateLimitService(t, rules)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	fastFirstTokenMS := 1_000
	require.False(t, svc.ObserveFirstTokenLatency(context.Background(), account, "request-0", &fastFirstTokenMS))
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
		{WindowMinutes: 1, ThresholdSeconds: 60, TriggerPercent: 50, PauseMinutes: 60},
		{WindowMinutes: 1, ThresholdSeconds: 15, TriggerPercent: 50, PauseMinutes: 30},
		{WindowMinutes: 1, ThresholdSeconds: 120, TriggerPercent: 50, PauseMinutes: 360},
	}
	svc, repo, counter := newFirstTokenLatencyRateLimitService(t, rules)
	account := &Account{ID: 43, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}
	fastFirstTokenMS := 1_000
	require.False(t, svc.ObserveFirstTokenLatency(context.Background(), account, "request-1", &fastFirstTokenMS))
	firstTokenMS := 130_000

	blocked := svc.ObserveFirstTokenLatency(context.Background(), account, "request-2", &firstTokenMS)

	require.True(t, blocked)
	require.Len(t, counter.recordedKeys, 6)
	require.Equal(t, 360*60, counter.claimSeconds)
	require.Contains(t, repo.lastTempReason, `"rule_index":2`)
	require.Contains(t, repo.lastTempReason, `"pause_minutes":360`)
	require.Contains(t, repo.lastTempReason, `"sample_count":2`)
	require.Contains(t, repo.lastTempReason, `"slow_sample_count":1`)
	require.Contains(t, repo.lastTempReason, `"observed_percent":50`)
	require.Contains(t, repo.lastTempReason, `"trigger_percent":50`)
}

func TestObserveFirstTokenLatency_ExactThresholdDoesNotCount(t *testing.T) {
	rules := []FirstTokenLatencyAutoPauseRule{
		{WindowMinutes: 5, ThresholdSeconds: 10, TriggerPercent: 50, PauseMinutes: 5},
	}
	svc, repo, counter := newFirstTokenLatencyRateLimitService(t, rules)
	account := &Account{ID: 44, Platform: PlatformGrok, Status: StatusActive, Schedulable: true}
	firstTokenMS := 10_000

	blocked := svc.ObserveFirstTokenLatency(context.Background(), account, "request-3", &firstTokenMS)

	require.False(t, blocked)
	require.Len(t, counter.recordedKeys, 1)
	require.Equal(t, FirstTokenLatencySampleCounts{Total: 1, Slow: 0}, counter.counts[firstTokenLatencyRuleKey(rules[0])])
	require.Zero(t, repo.tempCalls)
}
