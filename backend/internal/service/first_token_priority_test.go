package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type staticFirstTokenLatencyStatsCache struct {
	stats        map[int64]FirstTokenLatencyStats
	claimAllowed bool
	claimedID    int64
	fetchedIDs   []int64
	recordedID   int64
}

func (c *staticFirstTokenLatencyStatsCache) RecordSample(_ context.Context, accountID int64, _ string, _ int) error {
	c.recordedID = accountID
	return nil
}

func (c *staticFirstTokenLatencyStatsCache) GetStatsBatch(_ context.Context, accountIDs []int64) (map[int64]FirstTokenLatencyStats, error) {
	c.fetchedIDs = append([]int64(nil), accountIDs...)
	return c.stats, nil
}

func (c *staticFirstTokenLatencyStatsCache) TryClaimProbe(_ context.Context, accountID int64, _ time.Duration) (bool, error) {
	c.claimedID = accountID
	return c.claimAllowed, nil
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
			name: "crossing ten seconds overrides near tie and baseline",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(10_100, 5, time.Minute),
				2: stats(9_900, 5, time.Minute),
			},
			expected: []int64{2, 1},
		},
		{
			name: "fast pool keeps low rate order while slow account stays behind",
			ids:  []int64{1, 2, 3},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(5_000, 5, time.Minute),
				2: stats(8_000, 5, time.Minute),
				3: stats(30_000, 5, time.Minute),
			},
			expected: []int64{1, 2, 3},
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
			name: "unknown account stays behind the fast pool",
			ids:  []int64{1, 2, 3},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(8_000, 5, time.Minute),
				2: stats(2_000, 5, time.Minute),
			},
			expected: []int64{1, 2, 3},
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

func TestFirstTokenPriorityOrderUsesSharedLeaseForDueProbe(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{
		claimAllowed: true,
		stats: map[int64]FirstTokenLatencyStats{
			1: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
			2: {PredictedMS: 60_000, SampleCount: 5, SlowStreak: 8, UpdatedAt: now.Add(-firstTokenPriorityProbeMax)},
		},
	}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{2, 1}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Equal(t, int64(2), cache.claimedID)

	cache.claimAllowed = false
	require.Equal(t, []int64{1, 2}, firstTokenPriorityOrder(context.Background(), accounts, cache))
}

func TestFirstTokenPriorityOrderImmediatelyPromotesFirstReliableFastProbeFromSlowPool(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 18_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 25_000, SampleCount: 5, UpdatedAt: now},
		3: {PredictedMS: 7_500, SampleCount: 1, UpdatedAt: now, ReliableFast: true},
	}}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{3, 1, 2}, firstTokenPriorityOrder(context.Background(), accounts, cache))
}

func TestFirstTokenPriorityOrderExcludesOAuthFromStatsAndUsesItAsFallback(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 20_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
	}}
	accounts := []*Account{
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{2, 1, 3}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Equal(t, []int64{1, 2}, cache.fetchedIDs)
}

func TestFirstTokenPriorityOrderPreservesLowRateBaselineIncludingOAuthWhenAllRelaysAreFast(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 8_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
	}}
	accounts := []*Account{
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{3, 1, 2}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Equal(t, []int64{1, 2}, cache.fetchedIDs)
}

func TestFirstTokenPriorityOrderKeepsAdaptiveProbeWhenAllRelaysAreFast(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{claimAllowed: true, stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 8_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now.Add(-10 * time.Minute)},
	}}
	accounts := []*Account{
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{2, 3, 1}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Equal(t, int64(2), cache.claimedID)
}

func TestObserveFirstTokenLatencyOnlyRecordsEnabledOpenAIAPIKeys(t *testing.T) {
	cache := &staticFirstTokenLatencyStatsCache{}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	latency := 1_000
	oauth := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	disabled := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled, Schedulable: true}
	relay := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	svc.ObserveFirstTokenLatency(context.Background(), oauth, "oauth", &latency)
	svc.ObserveFirstTokenLatency(context.Background(), disabled, "disabled", &latency)
	require.Zero(t, cache.recordedID)
	svc.ObserveFirstTokenLatency(context.Background(), relay, "relay", &latency)
	require.Equal(t, relay.ID, cache.recordedID)
}

func TestOpenAIFirstTokenPriorityFallsBackToLowRateWhenAllAccountsAreFast(t *testing.T) {
	now := time.Now()
	cheap := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.05, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 0.50, now.Add(-time.Minute), 30*time.Minute)
	order := []openAIAccountCandidateScore{{account: expensive}, {account: cheap}}
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		expensive.ID: {PredictedMS: 1_000, SampleCount: 5, UpdatedAt: now},
		cheap.ID:     {PredictedMS: 9_000, SampleCount: 5, UpdatedAt: now},
	}}
	got := applyOpenAIFirstTokenPriorityOrder(
		context.Background(),
		OpenAIAccountScheduleRequest{UseUpstreamTokenCost: true},
		order,
		cache,
		defaultOpenAIOAuthSchedulingRateMultiplier,
	)

	require.Equal(t, []int64{cheap.ID, expensive.ID}, []int64{got[0].account.ID, got[1].account.ID})
}

func TestOpenAIFirstTokenPriorityUsesLowRateWithinFastPool(t *testing.T) {
	now := time.Now()
	cheapFast := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.05, now.Add(-time.Minute), 30*time.Minute)
	expensiveFast := upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 0.50, now.Add(-time.Minute), 30*time.Minute)
	cheapSlow := upstreamCostTestAccount(3, UpstreamBillingProbeStatusOK, 0.04, now.Add(-time.Minute), 30*time.Minute)
	for _, account := range []*Account{cheapFast, expensiveFast, cheapSlow} {
		account.Status = StatusActive
		account.Schedulable = true
	}
	order := []openAIAccountCandidateScore{{account: expensiveFast}, {account: cheapSlow}, {account: cheapFast}}
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		expensiveFast.ID: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
		cheapFast.ID:     {PredictedMS: 9_000, SampleCount: 5, UpdatedAt: now},
		cheapSlow.ID:     {PredictedMS: 30_000, SampleCount: 5, UpdatedAt: now},
	}}

	got := applyOpenAIFirstTokenPriorityOrder(
		context.Background(),
		OpenAIAccountScheduleRequest{UseUpstreamTokenCost: true},
		order,
		cache,
		defaultOpenAIOAuthSchedulingRateMultiplier,
	)

	require.Equal(t, []int64{cheapFast.ID, expensiveFast.ID, cheapSlow.ID}, []int64{
		got[0].account.ID,
		got[1].account.ID,
		got[2].account.ID,
	})
}

func TestAccountFirstTokenLatencyMetricsOnlyIncludesEnabledOpenAIAPIKeys(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 4_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 2_000, SampleCount: 5, UpdatedAt: now},
		3: {PredictedMS: 3_000, SampleCount: 5, UpdatedAt: now},
		4: {PredictedMS: 1_000, SampleCount: 5, UpdatedAt: now},
	}}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	accounts := []Account{
		{ID: 1, Name: "relay", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Name: "oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		{ID: 3, Name: "disabled", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled, Schedulable: true},
		{ID: 4, Name: "not schedulable", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: false},
	}

	metrics, err := svc.AccountFirstTokenLatencyMetrics(context.Background(), accounts)
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, int64(1), metrics[0].AccountID)
	require.Equal(t, "relay", metrics[0].AccountName)
}
