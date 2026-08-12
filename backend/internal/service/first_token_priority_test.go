package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type staticFirstTokenLatencyStatsCache struct {
	stats map[int64]FirstTokenLatencyStats
}

func (c staticFirstTokenLatencyStatsCache) RecordSample(context.Context, int64, string, int) error {
	return nil
}

func (c staticFirstTokenLatencyStatsCache) GetStatsBatch(context.Context, []int64) (map[int64]FirstTokenLatencyStats, error) {
	return c.stats, nil
}

func TestFirstTokenPriorityOrderWithStats(t *testing.T) {
	now := time.Now()
	stats := func(predicted float64, samples int64, age time.Duration) FirstTokenLatencyStats {
		return FirstTokenLatencyStats{PredictedMS: predicted, SampleCount: samples, UpdatedAt: now.Add(-age)}
	}

	tests := []struct {
		name     string
		ids      []int64
		stats    map[int64]FirstTokenLatencyStats
		explore  bool
		expected []int64
	}{
		{
			name: "slower than ten seconds enables latency priority",
			ids:  []int64{1, 2, 3},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(13_000, 5, time.Minute),
				2: stats(4_000, 5, time.Minute),
				3: stats(8_000, 5, time.Minute),
			},
			expected: []int64{2, 3, 1},
		},
		{
			name: "near tie preserves baseline",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(12_400, 5, time.Minute),
				2: stats(12_000, 5, time.Minute),
			},
			expected: []int64{1, 2},
		},
		{
			name: "all reliably fast preserves low rate baseline",
			ids:  []int64{3, 1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(1_000, 5, time.Minute),
				2: stats(9_999, 5, time.Minute),
				3: stats(10_000, 5, time.Minute),
			},
			expected: []int64{3, 1, 2},
		},
		{
			name: "fast accounts only leave baseline when adaptive probe is due",
			ids:  []int64{3, 1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(1_000, 5, 10*time.Second),
				2: stats(9_000, 5, 10*time.Second),
				3: stats(8_000, 5, 10*time.Second),
			},
			explore:  true,
			expected: []int64{3, 1, 2},
		},
		{
			name: "overdue fast alternative receives adaptive probe",
			ids:  []int64{3, 1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(1_000, 5, 5*time.Minute),
				2: stats(9_000, 5, 10*time.Second),
				3: stats(8_000, 5, 10*time.Second),
			},
			explore:  true,
			expected: []int64{1, 3, 2},
		},
		{
			name: "unknown account prevents fast bypass",
			ids:  []int64{1, 2, 3},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(8_000, 5, time.Minute),
				2: stats(2_000, 5, time.Minute),
			},
			expected: []int64{2, 1, 3},
		},
		{
			name: "stale account prevents fast bypass",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(8_000, 5, firstTokenPriorityFreshFor+time.Second),
				2: stats(2_000, 5, time.Minute),
			},
			expected: []int64{2, 1},
		},
		{
			name: "insufficient samples are explored when overdue",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(12_000, 5, time.Minute),
				2: stats(3_000, 1, 3*time.Minute),
			},
			explore:  true,
			expected: []int64{2, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, firstTokenPriorityOrderWithStats(tt.ids, tt.stats, now, tt.explore))
		})
	}
}

func TestFirstTokenPriorityProbeIntervalBacksOffSlowAccounts(t *testing.T) {
	base := firstTokenPriorityProbeInterval(FirstTokenLatencyStats{PredictedMS: 20_000, SampleCount: 5}, 5_000)
	withStreak := firstTokenPriorityProbeInterval(FirstTokenLatencyStats{PredictedMS: 20_000, SampleCount: 5, SlowStreak: 4}, 5_000)
	require.Greater(t, withStreak, base)
	require.Equal(t, firstTokenPriorityProbeMax, firstTokenPriorityProbeInterval(
		FirstTokenLatencyStats{PredictedMS: 1_000_000, SampleCount: 5, SlowStreak: 20},
		1_000,
	))
}

func TestFirstTokenPriorityExploreIsStable(t *testing.T) {
	seed := ""
	for index := 0; index < 1_000; index++ {
		candidate := time.Unix(int64(index), 0).String()
		if firstTokenPriorityExplore(candidate) {
			seed = candidate
			break
		}
	}
	require.NotEmpty(t, seed)
	require.True(t, firstTokenPriorityExplore(seed))
	require.Equal(t, firstTokenPriorityExplore(seed), firstTokenPriorityExplore(seed))
	require.NotPanics(t, func() { _ = firstTokenPrioritySeed(context.Background(), []int64{1, 2}) })
}

func TestOpenAIFirstTokenPriorityFallsBackToLowRateWhenAllAccountsAreFast(t *testing.T) {
	now := time.Now()
	cheap := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.05, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 0.50, now.Add(-time.Minute), 30*time.Minute)
	order := []openAIAccountCandidateScore{{account: expensive}, {account: cheap}}
	cache := staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		expensive.ID: {PredictedMS: 1_000, SampleCount: 5, UpdatedAt: now},
		cheap.ID:     {PredictedMS: 9_000, SampleCount: 5, UpdatedAt: now},
	}}
	requestID := ""
	for index := 0; index < 100; index++ {
		candidate := time.Unix(int64(index), 0).String()
		if !firstTokenPriorityExplore(candidate + ":1:2") {
			requestID = candidate
			break
		}
	}
	require.NotEmpty(t, requestID)

	got := applyOpenAIFirstTokenPriorityOrder(
		context.WithValue(context.Background(), ctxkey.RequestID, requestID),
		OpenAIAccountScheduleRequest{UseUpstreamTokenCost: true},
		order,
		cache,
		defaultOpenAIOAuthSchedulingRateMultiplier,
	)

	require.Equal(t, []int64{cheap.ID, expensive.ID}, []int64{got[0].account.ID, got[1].account.ID})
}
