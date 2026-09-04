package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCurrentTotalDurationSettingsUsesValidatedDynamicValues(t *testing.T) {
	previous := CurrentTotalDurationSettings()
	t.Cleanup(func() {
		setCurrentTotalDurationSettings(previous.FastThresholdSeconds, previous.SlowThresholdSeconds, previous.SampleLimit, previous.MinimumSamples, previous.PrimaryWindowHours)
	})

	setCurrentTotalDurationSettings(14, 20, 80, 24, 8)
	got := CurrentTotalDurationSettings()
	require.Equal(t, TotalDurationSettings{
		FastThresholdSeconds: 14,
		SlowThresholdSeconds: 20,
		SampleLimit:          80,
		MinimumSamples:       24,
		PrimaryWindowHours:   8,
	}, got)

	setCurrentTotalDurationSettings(14, 20, 5, 6, 8)
	require.Equal(t, got, CurrentTotalDurationSettings(), "invalid minimum/sample-limit relation must not replace the active configuration")
}

type staticFirstTokenLatencyStatsCache struct {
	stats          map[int64]FirstTokenLatencyStats
	claimAllowed   bool
	claimResults   map[int64]bool
	claimedID      int64
	claimedIDs     []int64
	fetchedIDs     []int64
	recordedID     int64
	manualProbeID  int64
	manualRequests []int64
}

type accountCacheStatsUsageRepo struct {
	UsageLogRepository
	stats map[int64]AccountCacheStats
}

func (r accountCacheStatsUsageRepo) GetAccountCacheStatsBatch(_ context.Context, _ []int64, _, _ time.Time) (map[int64]AccountCacheStats, error) {
	return r.stats, nil
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
	c.claimedIDs = append(c.claimedIDs, accountID)
	if c.claimResults != nil {
		return c.claimResults[accountID], nil
	}
	return c.claimAllowed, nil
}

func (c *staticFirstTokenLatencyStatsCache) RequestManualProbe(_ context.Context, accountID int64, _ time.Duration) error {
	c.manualRequests = append(c.manualRequests, accountID)
	c.manualProbeID = accountID
	return nil
}

func (c *staticFirstTokenLatencyStatsCache) TryClaimManualProbe(_ context.Context, accountIDs []int64, _ time.Duration) (int64, bool, error) {
	for _, accountID := range accountIDs {
		if accountID == c.manualProbeID {
			c.manualProbeID = 0
			return accountID, true, nil
		}
	}
	return 0, false, nil
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
			name: "slow account enables total duration priority",
			ids:  []int64{1, 2, 3},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(13_000, 5, time.Minute),
				2: stats(4_000, 5, time.Minute),
				3: stats(8_000, 5, time.Minute),
			},
			expected: []int64{2, 3, 1},
		},
		{
			name: "slow pool near tie preserves baseline",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(14_400, 5, time.Minute),
				2: stats(14_000, 5, time.Minute),
			},
			expected: []int64{1, 2},
		},
		{
			name: "slow pool needs material advantage before reranking",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(15_000, 5, time.Minute),
				2: stats(12_500, 5, time.Minute),
			},
			expected: []int64{1, 2},
		},
		{
			name: "slow pool near tie preserves caller baseline order",
			ids:  []int64{2, 1},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(15_000, 5, time.Minute),
				2: stats(12_500, 5, time.Minute),
			},
			expected: []int64{2, 1},
		},
		{
			name: "slow pool reranks on material advantage",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(16_000, 5, time.Minute),
				2: stats(12_500, 5, time.Minute),
			},
			expected: []int64{2, 1},
		},
		{
			name: "crossing twelve seconds creates a separate fast pool",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(12_100, 5, time.Minute),
				2: stats(11_900, 5, time.Minute),
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
			name: "overdue fast alternative does not displace low rate winner",
			ids:  []int64{3, 1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(1_000, 5, 5*time.Minute),
				2: stats(9_000, 5, 10*time.Second),
				3: stats(8_000, 5, 10*time.Second),
			},
			explore:  true,
			expected: []int64{3, 1, 2},
		},
		{
			name: "probe skips overdue fast alternative and selects slow account",
			ids:  []int64{1, 2, 3},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(5_000, 5, 10*time.Second),
				2: stats(8_000, 5, 5*time.Minute),
				3: stats(30_000, 5, 2*time.Hour),
			},
			explore:  true,
			expected: []int64{3, 1, 2},
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
		{
			name: "unconfirmed recovery cannot enter baseline fast pool",
			ids:  []int64{1, 2},
			stats: map[int64]FirstTokenLatencyStats{
				1: stats(18_000, 9, time.Minute),
				2: {
					PredictedMS:             7_000,
					SampleCount:             9,
					UpdatedAt:               now.Add(-time.Minute),
					FastConfirmationTracked: true,
				},
			},
			expected: []int64{1, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, firstTokenPriorityOrderWithStats(tt.ids, tt.stats, now, tt.explore))
		})
	}
}

func TestFirstTokenPriorityStatsFastTrustsTrackedPoolStateUntilWindowExit(t *testing.T) {
	now := time.Now()
	tracked := FirstTokenLatencyStats{
		PredictedMS:             15_999,
		SampleCount:             50,
		UpdatedAt:               now,
		ReliableFast:            true,
		FastConfirmationTracked: true,
	}
	require.True(t, firstTokenPriorityStatsFast(tracked, now), "tracked pool membership must not exit at the entry threshold")

	tracked.ReliableFast = false
	require.False(t, firstTokenPriorityStatsFast(tracked, now))

	legacy := tracked
	legacy.ReliableFast = true
	legacy.FastConfirmationTracked = false
	require.False(t, firstTokenPriorityStatsFast(legacy, now), "legacy untracked stats still use the twelve-second threshold")
}

func TestFirstTokenPriorityProbeIntervalBacksOffSlowAccounts(t *testing.T) {
	base := firstTokenPriorityProbeInterval(FirstTokenLatencyStats{PredictedMS: 20_000, SampleCount: 20}, 5_000)
	withStreak := firstTokenPriorityProbeInterval(FirstTokenLatencyStats{PredictedMS: 20_000, SampleCount: 20, SlowStreak: 4}, 5_000)
	require.Greater(t, withStreak, base)
	require.Equal(t, firstTokenPriorityProbeStairMax, firstTokenPriorityProbeInterval(
		FirstTokenLatencyStats{PredictedMS: 1_000_000, SampleCount: 20, SlowStreak: 20},
		1_000,
	))
	require.Equal(t, firstTokenPriorityRecoveryProbe, firstTokenPriorityProbeInterval(
		FirstTokenLatencyStats{PredictedMS: 18_000, SampleCount: 5, RecoveryFastStreak: 1},
		5_000,
	))
	require.Equal(t, firstTokenPriorityRecoveryProbe, firstTokenPriorityProbeInterval(
		FirstTokenLatencyStats{PredictedMS: 7_000, SampleCount: 1, RecoveryFastStreak: 1},
		5_000,
	))
	require.Equal(t, firstTokenPriorityRecoveryProbe, firstTokenPriorityProbeInterval(
		FirstTokenLatencyStats{PredictedMS: 7_000, SampleCount: 20, CircuitBroken: true},
		5_000,
	))
}

func TestFirstTokenPriorityStatsFastRequiresTrackedConfirmation(t *testing.T) {
	now := time.Now()
	recovering := FirstTokenLatencyStats{
		PredictedMS:             7_000,
		SampleCount:             20,
		UpdatedAt:               now,
		FastConfirmationTracked: true,
		RecoveryFastStreak:      2,
	}
	require.False(t, firstTokenPriorityStatsFast(recovering, now))
	recovering.ReliableFast = true
	recovering.RecoveryFastStreak = 0
	require.True(t, firstTokenPriorityStatsFast(recovering, now))
	recovering.CircuitBroken = true
	require.False(t, firstTokenPriorityStatsFast(recovering, now))
}

func TestFirstTokenPriorityCircuitBrokenAccountEntersSlowPool(t *testing.T) {
	now := time.Now()
	stats := map[int64]FirstTokenLatencyStats{
		1: {
			PredictedMS:             6_000,
			SampleCount:             20,
			UpdatedAt:               now,
			FastConfirmationTracked: true,
			CircuitBroken:           true,
		},
		2: {
			PredictedMS:             8_000,
			SampleCount:             20,
			UpdatedAt:               now,
			ReliableFast:            true,
			FastConfirmationTracked: true,
		},
	}

	require.Equal(t, []int64{2, 1}, firstTokenPriorityOrderWithStats([]int64{1, 2}, stats, now, false))
}

func TestFirstTokenPriorityDefaultStickyEligible(t *testing.T) {
	now := time.Now()
	reliable := FirstTokenLatencyStats{PredictedMS: 12_000, SampleCount: 3, UpdatedAt: now}
	require.True(t, firstTokenPriorityDefaultStickyEligible(reliable, now))

	reliable.PredictedMS = 12_001
	require.False(t, firstTokenPriorityDefaultStickyEligible(reliable, now))

	circuitBroken := FirstTokenLatencyStats{
		PredictedMS:             12_000,
		SampleCount:             20,
		UpdatedAt:               now,
		ReliableFast:            true,
		FastConfirmationTracked: true,
		CircuitBroken:           true,
	}
	require.False(t, firstTokenPriorityDefaultStickyEligible(circuitBroken, now), "circuit-broken account must not bypass the slow pool through hard session affinity")
	circuitBroken.CircuitBroken = false
	require.True(t, firstTokenPriorityDefaultStickyEligible(circuitBroken, now))

	reliable.PredictedMS = 12_000
	reliable.SampleCount = 2
	require.False(t, firstTokenPriorityDefaultStickyEligible(reliable, now))

	reliable.SampleCount = 3
	reliable.UpdatedAt = now.Add(-firstTokenPriorityFreshFor - time.Second)
	require.False(t, firstTokenPriorityDefaultStickyEligible(reliable, now))
}

func TestApplyOpenAIFirstTokenStickyOrderReusesLegacyWeightedPolicy(t *testing.T) {
	now := time.Now()
	cheap := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	equalSticky := upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	expensiveSticky := upstreamCostTestAccount(3, UpstreamBillingProbeStatusOK, 0.08, now.Add(-time.Minute), 30*time.Minute)
	slowProbe := upstreamCostTestAccount(4, UpstreamBillingProbeStatusOK, 0.02, now.Add(-time.Minute), 30*time.Minute)
	for _, account := range []*Account{cheap, equalSticky, expensiveSticky, slowProbe} {
		account.Status = StatusActive
		account.Schedulable = true
	}
	stats := map[int64]FirstTokenLatencyStats{
		cheap.ID:           {PredictedMS: 20_000, SampleCount: 5, UpdatedAt: now},
		equalSticky.ID:     {PredictedMS: 25_000, SampleCount: 5, UpdatedAt: now},
		expensiveSticky.ID: {PredictedMS: 18_000, SampleCount: 5, UpdatedAt: now},
		slowProbe.ID:       {PredictedMS: 30_000, SampleCount: 5, UpdatedAt: now},
	}
	cache := &staticFirstTokenLatencyStatsCache{stats: stats}
	rateOrder := newOpenAILegacyUpstreamRateOrder([]*Account{cheap, equalSticky, expensiveSticky, slowProbe}, now, defaultOpenAIOAuthSchedulingRateMultiplier)

	winsWithStickyWeight := 0
	for seed := uint64(1); seed <= 1_000; seed++ {
		req := OpenAIAccountScheduleRequest{StickyAccountID: equalSticky.ID, SessionHash: fmt.Sprintf("session-%d", seed), RequestedModel: "gpt-test"}
		actual := []openAIAccountCandidateScore{{account: cheap}, {account: equalSticky}, {account: expensiveSticky}}
		expected := append([]openAIAccountCandidateScore(nil), actual...)
		applyOpenAIFirstTokenStickyOrder(context.Background(), actual, req, cache, rateOrder)
		applyOpenAILegacySoftStickyOrder(
			expected,
			func(candidate openAIAccountCandidateScore) *Account { return candidate.account },
			rateOrder,
			openAILegacySoftStickyPolicy{
				enabled:   true,
				accountID: equalSticky.ID,
				weight:    openAILegacySessionStickyWeight,
				seed:      deriveOpenAISelectionSeed(req),
			},
			func(account *Account) int {
				if account != nil && firstTokenPriorityStatsFast(stats[account.ID], now) {
					return 1
				}
				return 0
			},
		)
		require.Equal(t, candidateAccountIDs(expected), candidateAccountIDs(actual))
		if actual[0].account.ID == equalSticky.ID {
			winsWithStickyWeight++
		}
	}
	require.Positive(t, winsWithStickyWeight)
	require.Less(t, winsWithStickyWeight, 1_000)

	higherRate := []openAIAccountCandidateScore{{account: cheap}, {account: expensiveSticky}}
	applyOpenAIFirstTokenStickyOrder(context.Background(), higherRate, OpenAIAccountScheduleRequest{StickyAccountID: expensiveSticky.ID}, cache, rateOrder)
	require.Equal(t, []int64{cheap.ID, expensiveSticky.ID}, candidateAccountIDs(higherRate))

	probeFirst := []openAIAccountCandidateScore{{account: slowProbe}, {account: cheap}, {account: equalSticky}}
	applyOpenAIFirstTokenStickyOrder(context.Background(), probeFirst, OpenAIAccountScheduleRequest{StickyAccountID: equalSticky.ID}, cache, rateOrder)
	require.Equal(t, slowProbe.ID, probeFirst[0].account.ID)
	require.ElementsMatch(t, []int64{cheap.ID, equalSticky.ID}, candidateAccountIDs(probeFirst[1:]))

}

func TestApplyOpenAIFirstTokenStickyOrderWeightsReliableSlowSession(t *testing.T) {
	now := time.Now()
	cheapSlow := upstreamCostTestAccount(21, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	stickySlow := upstreamCostTestAccount(22, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	fast := upstreamCostTestAccount(23, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	for _, account := range []*Account{cheapSlow, stickySlow, fast} {
		account.Status = StatusActive
		account.Schedulable = true
	}
	stats := map[int64]FirstTokenLatencyStats{
		cheapSlow.ID:  {PredictedMS: 20_000, SampleCount: 5, UpdatedAt: now},
		stickySlow.ID: {PredictedMS: 25_000, SampleCount: 5, UpdatedAt: now},
		fast.ID:       {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
	}
	cache := &staticFirstTokenLatencyStatsCache{stats: stats}
	rateOrder := newOpenAILegacyUpstreamRateOrder([]*Account{cheapSlow, stickySlow, fast}, now, defaultOpenAIOAuthSchedulingRateMultiplier)
	ordered := []openAIAccountCandidateScore{{account: fast}, {account: cheapSlow}, {account: stickySlow}}
	applyOpenAIFirstTokenStickyOrder(context.Background(), ordered, OpenAIAccountScheduleRequest{StickyAccountID: stickySlow.ID, SessionHash: "slow-session"}, cache, rateOrder)
	require.Equal(t, fast.ID, ordered[0].account.ID, "slow sticky must not cross the confirmed fast pool")
	require.ElementsMatch(t, []int64{cheapSlow.ID, stickySlow.ID}, candidateAccountIDs(ordered[1:]), "weighted sticky remains inside the slow pool")

	ordered = []openAIAccountCandidateScore{{account: cheapSlow}, {account: stickySlow}}
	wins := 0
	for seed := uint64(1); seed <= 500; seed++ {
		candidateOrder := append([]openAIAccountCandidateScore(nil), ordered...)
		applyOpenAIFirstTokenStickyOrder(context.Background(), candidateOrder, OpenAIAccountScheduleRequest{StickyAccountID: stickySlow.ID, SessionHash: fmt.Sprintf("slow-session-%d", seed)}, cache, rateOrder)
		if candidateOrder[0].account.ID == stickySlow.ID {
			wins++
		}
	}
	require.Greater(t, wins, 0)
	require.Less(t, wins, 500)
}

func TestFirstTokenProbePreservesHealthyFastStickyBinding(t *testing.T) {
	now := time.Now()
	probe := upstreamCostTestAccount(11, UpstreamBillingProbeStatusOK, 0.02, now.Add(-time.Minute), 30*time.Minute)
	fast := upstreamCostTestAccount(12, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	for _, account := range []*Account{probe, fast} {
		account.Status = StatusActive
		account.Schedulable = true
	}
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		probe.ID: {PredictedMS: 30_000, SampleCount: 5, UpdatedAt: now},
		fast.ID:  {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
	}}
	rateOrder := newOpenAILegacyUpstreamRateOrder([]*Account{probe, fast}, now, defaultOpenAIOAuthSchedulingRateMultiplier)
	ordered := []openAIAccountCandidateScore{{account: probe}, {account: fast}}
	applyOpenAIFirstTokenStickyOrder(context.Background(), ordered, OpenAIAccountScheduleRequest{StickyAccountID: fast.ID}, cache, rateOrder)
	require.Equal(t, []int64{probe.ID, fast.ID}, candidateAccountIDs(ordered))
}

func TestFirstTokenProbeOnlyReordersFreshScheduling(t *testing.T) {
	now := time.Now()
	probe := upstreamCostTestAccount(31, UpstreamBillingProbeStatusOK, 0.02, now.Add(-time.Minute), 30*time.Minute)
	sticky := upstreamCostTestAccount(32, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	for _, account := range []*Account{probe, sticky} {
		account.Status = StatusActive
		account.Schedulable = true
	}
	cache := &staticFirstTokenLatencyStatsCache{
		claimAllowed: true,
		stats: map[int64]FirstTokenLatencyStats{
			probe.ID:  {PredictedMS: 30_000, SampleCount: 5, UpdatedAt: now.Add(-firstTokenPriorityProbeMax)},
			sticky.ID: {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
		},
	}

	ordered := []openAIAccountCandidateScore{{account: sticky}, {account: probe}}
	applyOpenAIFirstTokenPriorityOrder(context.Background(), OpenAIAccountScheduleRequest{
		FirstTokenProbeEligible: true,
		StickyAccountID:         sticky.ID,
	}, ordered, cache, defaultOpenAIOAuthSchedulingRateMultiplier)
	require.Equal(t, []int64{sticky.ID, probe.ID}, candidateAccountIDs(ordered))
	require.Empty(t, cache.claimedIDs, "existing sticky scheduling must not claim a dynamic probe")

	cache.claimedIDs = nil
	ordered = []openAIAccountCandidateScore{{account: sticky}, {account: probe}}
	applyOpenAIFirstTokenPriorityOrder(context.Background(), OpenAIAccountScheduleRequest{
		FirstTokenProbeEligible: true,
	}, ordered, cache, defaultOpenAIOAuthSchedulingRateMultiplier)
	require.Equal(t, []int64{probe.ID, sticky.ID}, candidateAccountIDs(ordered))
	require.Equal(t, []int64{probe.ID}, cache.claimedIDs)
}

func TestFirstTokenManualProbeWaitsForFreshScheduling(t *testing.T) {
	now := time.Now()
	probe := upstreamCostTestAccount(41, UpstreamBillingProbeStatusOK, 0.02, now.Add(-time.Minute), 30*time.Minute)
	sticky := upstreamCostTestAccount(42, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	for _, account := range []*Account{probe, sticky} {
		account.Status = StatusActive
		account.Schedulable = true
	}
	cache := &staticFirstTokenLatencyStatsCache{
		manualProbeID: probe.ID,
		stats: map[int64]FirstTokenLatencyStats{
			probe.ID:  {PredictedMS: 30_000, SampleCount: 5, UpdatedAt: now},
			sticky.ID: {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
		},
	}
	ordered := []openAIAccountCandidateScore{{account: sticky}, {account: probe}}
	applyOpenAIFirstTokenPriorityOrder(context.Background(), OpenAIAccountScheduleRequest{
		FirstTokenProbeEligible: true,
		StickyAccountID:         sticky.ID,
	}, ordered, cache, defaultOpenAIOAuthSchedulingRateMultiplier)
	require.Equal(t, []int64{sticky.ID, probe.ID}, candidateAccountIDs(ordered))
	require.Equal(t, probe.ID, cache.manualProbeID, "sticky scheduling must leave the manual probe queued")
	require.Empty(t, cache.claimedIDs)
}

func TestFirstTokenProbeGateTreatsPreviousResponseAsSticky(t *testing.T) {
	require.False(t, firstTokenProbeAllowedForNewScheduling(true, 0, 44))
	require.False(t, firstTokenProbeAllowedForNewScheduling(true, 44, 0))
	require.True(t, firstTokenProbeAllowedForNewScheduling(true, 0, 0))
	require.False(t, firstTokenProbeAllowedForNewScheduling(false, 0, 0))
}

func candidateAccountIDs(candidates []openAIAccountCandidateScore) []int64 {
	ids := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.account.ID)
	}
	return ids
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

func TestFirstTokenPriorityAllSlowPoolStillProbesFifthAccount(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{
		claimResults: map[int64]bool{5: true},
		stats: map[int64]FirstTokenLatencyStats{
			1: {PredictedMS: 20_000, SampleCount: 20, UpdatedAt: now},
			2: {PredictedMS: 24_000, SampleCount: 20, UpdatedAt: now},
			3: {PredictedMS: 26_000, SampleCount: 20, UpdatedAt: now},
			4: {PredictedMS: 28_000, SampleCount: 20, UpdatedAt: now},
			5: {PredictedMS: 60_000, SampleCount: 20, UpdatedAt: now.Add(-4 * time.Minute), SlowStreak: 20},
		},
	}
	accounts := make([]*Account, 0, 5)
	for id := int64(1); id <= 5; id++ {
		accounts = append(accounts, &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true})
	}

	cache.claimResults = map[int64]bool{5: false}
	ordered := firstTokenPriorityOrder(context.Background(), accounts, cache)
	require.Equal(t, []int64{1, 2, 3, 4, 5}, ordered, "a four-minute-old persistently slow account is not probed every three minutes")
	require.Zero(t, cache.claimedID)

	// The probe-only rotation must not replace the slow-pool duration order when
	// no account successfully claims the shared probe lease.
	cache.claimResults = map[int64]bool{5: false}
	reorderedAccounts := []*Account{accounts[4], accounts[0], accounts[3], accounts[1], accounts[2]}
	baselineOrder := firstTokenPriorityOrderWithProbe(context.Background(), reorderedAccounts, cache, false)
	ordered = firstTokenPriorityOrder(context.Background(), reorderedAccounts, cache)
	require.Equal(t, baselineOrder, ordered)

	cache.stats[5] = FirstTokenLatencyStats{PredictedMS: 60_000, SampleCount: 20, UpdatedAt: now.Add(-11 * time.Minute), SlowStreak: 20}
	cache.claimResults = map[int64]bool{5: true}
	ordered = firstTokenPriorityOrder(context.Background(), accounts, cache)
	require.Equal(t, []int64{5, 1, 2, 3, 4}, ordered, "an all-slow group must give an overdue fifth account a probe before reusing account one")
	require.Equal(t, int64(5), cache.claimedID)
}

func TestFirstTokenPriorityOrderManualProbeOverridesAllFastPool(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{
		manualProbeID: 2,
		stats: map[int64]FirstTokenLatencyStats{
			1: {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
			2: {PredictedMS: 7_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
		},
	}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{2, 1}, firstTokenPriorityOrderWithProbe(context.Background(), accounts, cache, true))
	require.Zero(t, cache.manualProbeID)
}

func TestFirstTokenPriorityOrderKeepsManualProbeQueuedForNonStreamingRequest(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{
		manualProbeID: 2,
		stats: map[int64]FirstTokenLatencyStats{
			1: {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
			2: {PredictedMS: 7_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
		},
	}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{1, 2}, firstTokenPriorityOrderWithProbe(context.Background(), accounts, cache, false))
	require.Equal(t, int64(2), cache.manualProbeID)
}

func TestRequestFirstTokenManualProbeValidatesAccount(t *testing.T) {
	cache := &staticFirstTokenLatencyStatsCache{}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	eligible := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	require.NoError(t, svc.RequestFirstTokenManualProbe(context.Background(), eligible))
	require.Equal(t, []int64{7}, cache.manualRequests)
	require.ErrorIs(t, svc.RequestFirstTokenManualProbe(context.Background(), &Account{
		ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
	}), ErrFirstTokenManualProbeIneligible)
}

func TestFirstTokenPriorityOrderSkipsProbeWhenRequestCannotProduceSample(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{
		claimAllowed: true,
		stats: map[int64]FirstTokenLatencyStats{
			1: {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
			2: {PredictedMS: 2_000, SampleCount: 1, UpdatedAt: now.Add(-time.Minute), RecoveryFastStreak: 1, FastConfirmationTracked: true},
		},
	}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{1, 2}, firstTokenPriorityOrderWithProbe(context.Background(), accounts, cache, false))
	require.Empty(t, cache.claimedIDs)
	require.Equal(t, []int64{2, 1}, firstTokenPriorityOrderWithProbe(context.Background(), accounts, cache, true))
	require.Equal(t, []int64{2}, cache.claimedIDs)
}

func TestFirstTokenProbeEligibilityRequiresExplicitStreamingMarker(t *testing.T) {
	require.False(t, firstTokenProbeEligible(context.Background()))
	require.False(t, firstTokenProbeEligible(WithFirstTokenProbeEligibility(context.Background(), false)))
	require.True(t, firstTokenProbeEligible(WithFirstTokenProbeEligibility(context.Background(), true)))
}

func TestFirstTokenPriorityOrderTriesNextDueProbeWhenLeaseIsAlreadyClaimed(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{
		claimResults: map[int64]bool{2: false, 3: true},
		stats: map[int64]FirstTokenLatencyStats{
			1: {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
			2: {PredictedMS: 60_000, SampleCount: 5, SlowStreak: 8, UpdatedAt: now.Add(-firstTokenPriorityProbeMax)},
			3: {PredictedMS: 40_000, SampleCount: 5, SlowStreak: 4, UpdatedAt: now.Add(-firstTokenPriorityProbeMax)},
		},
	}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{3, 1, 2}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Equal(t, []int64{2, 3}, cache.claimedIDs)
}

func TestFirstTokenPriorityOrderConfirmsRecoveringAccountBeforeGenericSlowProbe(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{
		claimAllowed: true,
		stats: map[int64]FirstTokenLatencyStats{
			1: {PredictedMS: 5_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
			2: {PredictedMS: 50_000, SampleCount: 1, UpdatedAt: now.Add(-3 * time.Hour), FastConfirmationTracked: true},
			3: {PredictedMS: 7_000, SampleCount: 1, UpdatedAt: now.Add(-31 * time.Second), RecoveryFastStreak: 1, FastConfirmationTracked: true},
		},
	}
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}

	require.Equal(t, []int64{3, 1, 2}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Equal(t, int64(3), cache.claimedID)
}

func TestFirstTokenPriorityOrderPromotesConfirmedFastRecoveryFromSlowPool(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 18_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 25_000, SampleCount: 5, UpdatedAt: now},
		3: {PredictedMS: 7_500, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
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

func TestFirstTokenPriorityOrderKeepsUnmeasuredOAuthBehindFastPool(t *testing.T) {
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

	require.Equal(t, []int64{1, 2, 3}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Equal(t, []int64{1, 2}, cache.fetchedIDs)
}

func TestFirstTokenPriorityOrderDoesNotProbeHigherRateRelayWhenAllRelaysAreFast(t *testing.T) {
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

	require.Equal(t, []int64{1, 2, 3}, firstTokenPriorityOrder(context.Background(), accounts, cache))
	require.Zero(t, cache.claimedID)
}

func TestObserveTotalDurationLatencyOnlyRecordsBillableOpenAIStreams(t *testing.T) {
	cache := &staticFirstTokenLatencyStatsCache{}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	durationMS := 9_000
	usage := func(requestID string) *UsageLog {
		return &UsageLog{RequestID: requestID, RequestType: RequestTypeStream, Stream: true, ActualCost: 0.01, DurationMs: &durationMS}
	}
	oauth := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	disabled := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled, Schedulable: true}
	relay := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	svc.ObserveTotalDurationLatency(context.Background(), oauth, usage("oauth"))
	svc.ObserveTotalDurationLatency(context.Background(), disabled, usage("disabled"))
	require.Zero(t, cache.recordedID)
	svc.ObserveTotalDurationLatency(context.Background(), relay, &UsageLog{RequestID: "sync", RequestType: RequestTypeSync, ActualCost: 0.01, DurationMs: &durationMS})
	svc.ObserveTotalDurationLatency(context.Background(), relay, &UsageLog{RequestID: "free", RequestType: RequestTypeStream, Stream: true, DurationMs: &durationMS})
	require.Zero(t, cache.recordedID)
	svc.ObserveTotalDurationLatency(context.Background(), relay, usage("relay"))
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

func TestOpenAIFirstTokenPriorityAlwaysUsesLowRateInsideFastPool(t *testing.T) {
	now := time.Now()
	cheap := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.05, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 0.50, now.Add(-time.Minute), 30*time.Minute)
	order := []openAIAccountCandidateScore{{account: expensive}, {account: cheap}}
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		expensive.ID: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
		cheap.ID:     {PredictedMS: 8_000, SampleCount: 5, UpdatedAt: now},
	}}

	got := applyOpenAIFirstTokenPriorityOrder(
		context.Background(),
		OpenAIAccountScheduleRequest{UseUpstreamTokenCost: false},
		order,
		cache,
		defaultOpenAIOAuthSchedulingRateMultiplier,
	)

	require.Equal(t, []int64{cheap.ID, expensive.ID}, candidateAccountIDs(got))
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
		1: {PredictedMS: 4_000, P50MS: 3_500, P90MS: 9_000, SampleCount: 20, WindowHours: 6, UpdatedAt: now},
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

func TestAccountFirstTokenLatencyMetricsIncludesRollingCacheRate(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 4_000, P50MS: 3_500, P90MS: 9_000, SampleCount: 20, WindowHours: 6, UpdatedAt: now},
	}}
	group := &Group{ID: 10, Name: "premium", Platform: PlatformOpenAI, Status: StatusActive}
	account := Account{
		ID: 1, Name: "relay", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		GroupIDs: []int64{group.ID}, Groups: []*Group{group},
		AccountGroups: []AccountGroup{{AccountID: 1, GroupID: group.ID, Group: group}},
	}
	svc := &RateLimitService{
		firstTokenLatencyStatsCache: cache,
		usageRepo: accountCacheStatsUsageRepo{stats: map[int64]AccountCacheStats{
			1: {CacheReadTokens: 75, CacheRateDenominator: 300},
		}},
	}

	metrics, err := svc.AccountFirstTokenLatencyMetrics(context.Background(), []Account{account})

	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.NotNil(t, metrics[0].CacheRate)
	require.InDelta(t, 0.25, *metrics[0].CacheRate, 1e-9)
	require.Equal(t, int64(75), metrics[0].CacheReadTokens)
	require.Equal(t, int64(300), metrics[0].CacheRateDenominator)
	require.Equal(t, 4_000.0, metrics[0].NormalTotalMS)
	require.Equal(t, 3_500.0, metrics[0].P50MS)
	require.Equal(t, 9_000.0, metrics[0].P90MS)
	require.Equal(t, 6, metrics[0].WindowHours)
}

func TestAccountFirstTokenLatencyMetricsReportsActualPoolMembership(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {
			PredictedMS:             4_000,
			SampleCount:             20,
			UpdatedAt:               now,
			ReliableFast:            true,
			FastConfirmationTracked: true,
		},
		2: {
			PredictedMS:             7_000,
			SampleCount:             20,
			UpdatedAt:               now,
			RecoveryFastStreak:      2,
			FastConfirmationTracked: true,
		},
		3: {
			PredictedMS:             5_000,
			SampleCount:             20,
			UpdatedAt:               now.Add(-firstTokenPriorityFreshFor - time.Minute),
			ReliableFast:            true,
			FastConfirmationTracked: true,
		},
		4: {
			PredictedMS:             3_000,
			SampleCount:             20,
			UpdatedAt:               now,
			ReliableFast:            true,
			FastConfirmationTracked: true,
		},
	}}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	group := &Group{ID: 10, Name: "premium", Platform: PlatformOpenAI, Status: StatusActive}
	inactiveGroup := &Group{ID: 11, Name: "disabled", Platform: PlatformOpenAI, Status: StatusDisabled}
	inactiveOnlyGroup := &Group{ID: 12, Name: "disabled-only", Platform: PlatformOpenAI, Status: StatusDisabled}
	confirmedFast := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.045, now.Add(-time.Minute), 30*time.Minute)
	confirmedFast.Name = "confirmed-fast"
	confirmedFast.Status = StatusActive
	confirmedFast.Schedulable = true
	confirmedFast.AccountGroups = []AccountGroup{
		{AccountID: 1, GroupID: group.ID, Group: group},
		{AccountID: 1, GroupID: inactiveGroup.ID, Group: inactiveGroup},
	}
	confirmedFast.GroupIDs = []int64{group.ID, inactiveGroup.ID}
	confirmedFast.Groups = []*Group{group, inactiveGroup}
	rateLimitedUntil := now.Add(time.Hour)
	accounts := []Account{
		*confirmedFast,
		{ID: 2, Name: "recovering", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 3, Name: "stale", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 4, Name: "rate-limited", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, RateLimitResetAt: &rateLimitedUntil},
		{ID: 5, Name: "pending-sample", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{
			ID: 6, Name: "inactive-group-only", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{inactiveOnlyGroup.ID}, Groups: []*Group{inactiveOnlyGroup},
			AccountGroups: []AccountGroup{{AccountID: 6, GroupID: inactiveOnlyGroup.ID, Group: inactiveOnlyGroup}},
		},
	}

	metrics, err := svc.AccountFirstTokenLatencyMetrics(context.Background(), accounts)
	require.NoError(t, err)
	require.Len(t, metrics, 4)

	poolByID := make(map[int64]bool, len(metrics))
	recoveryByID := make(map[int64]int, len(metrics))
	for _, metric := range metrics {
		poolByID[metric.AccountID] = metric.IsFastPool
		recoveryByID[metric.AccountID] = metric.RecoveryFastStreak
	}
	require.True(t, poolByID[1])
	require.False(t, poolByID[2], "recovering account remains in the slow pool until confirmed")
	require.Equal(t, 2, recoveryByID[2])
	require.False(t, poolByID[3], "stale prediction is not eligible for the fast pool")
	require.NotContains(t, poolByID, int64(4), "currently unschedulable account is excluded")
	require.NotContains(t, poolByID, int64(6), "account assigned only to an inactive group is not mislabeled as ungrouped")
	require.False(t, poolByID[5], "account without samples remains in the slow pool")
	require.False(t, metrics[3].HasPrediction)
	require.Equal(t, int64(30), metrics[3].ProbeIntervalSeconds)
	require.NotNil(t, metrics[0].SchedulingRateMultiplier)
	require.InDelta(t, 0.045, *metrics[0].SchedulingRateMultiplier, 1e-9)
	require.Equal(t, []AccountFirstTokenLatencyGroup{{GroupID: 10, GroupName: "premium"}}, metrics[0].Groups)
}

func TestAccountFirstTokenLatencyMetricsFiltersGroupsByDefaultProfitAdmission(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 4_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
		3: {PredictedMS: 6_000, SampleCount: 5, UpdatedAt: now},
	}}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	strict := &Group{
		ID: 10, Name: "strict", Platform: PlatformOpenAI, Status: StatusActive,
		RateMultiplier: 0.12, ProfitControlEnabled: true, ProfitMinMargin: 0.15,
	}
	permissive := &Group{
		ID: 20, Name: "permissive", Platform: PlatformOpenAI, Status: StatusActive,
		RateMultiplier: 0.20, ProfitControlEnabled: true, ProfitMinMargin: 0.15,
	}
	disabledGate := &Group{
		ID: 30, Name: "disabled-gate", Platform: PlatformOpenAI, Status: StatusActive,
		RateMultiplier: 0.01, ProfitControlEnabled: false,
	}
	rate006 := 0.06
	rate011 := 0.11
	rate100 := 1.0
	accounts := []Account{
		{
			ID: 1, Name: "eligible", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, RateMultiplier: &rate006,
			GroupIDs: []int64{strict.ID}, Groups: []*Group{strict},
			AccountGroups: []AccountGroup{{AccountID: 1, GroupID: strict.ID, Group: strict}},
		},
		{
			ID: 2, Name: "multi-group", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, RateMultiplier: &rate011,
			GroupIDs: []int64{strict.ID, permissive.ID}, Groups: []*Group{strict, permissive},
			AccountGroups: []AccountGroup{
				{AccountID: 2, GroupID: strict.ID, Group: strict},
				{AccountID: 2, GroupID: permissive.ID, Group: permissive},
			},
		},
		{
			ID: 3, Name: "profit-disabled", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, RateMultiplier: &rate100,
			GroupIDs: []int64{disabledGate.ID}, Groups: []*Group{disabledGate},
			AccountGroups: []AccountGroup{{AccountID: 3, GroupID: disabledGate.ID, Group: disabledGate}},
		},
	}

	metrics, err := svc.AccountFirstTokenLatencyMetrics(context.Background(), accounts)
	require.NoError(t, err)
	require.Len(t, metrics, 3)
	groupsByID := make(map[int64][]AccountFirstTokenLatencyGroup, len(metrics))
	for _, metric := range metrics {
		groupsByID[metric.AccountID] = metric.Groups
	}
	require.Equal(t, []AccountFirstTokenLatencyGroup{{GroupID: strict.ID, GroupName: strict.Name}}, groupsByID[1])
	require.Equal(t, []AccountFirstTokenLatencyGroup{{GroupID: permissive.ID, GroupName: permissive.Name}}, groupsByID[2])
	require.Equal(t, []AccountFirstTokenLatencyGroup{{GroupID: disabledGate.ID, GroupName: disabledGate.Name}}, groupsByID[3])

	onlyRejected := accounts[1]
	onlyRejected.GroupIDs = []int64{strict.ID}
	onlyRejected.Groups = []*Group{strict}
	onlyRejected.AccountGroups = []AccountGroup{{AccountID: 2, GroupID: strict.ID, Group: strict}}
	metrics, err = svc.AccountFirstTokenLatencyMetrics(context.Background(), []Account{onlyRejected})
	require.NoError(t, err)
	require.Empty(t, metrics)
}

func TestAccountFirstTokenLatencyMetricsHidesDedicatedDashboardEntries(t *testing.T) {
	now := time.Now()
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{
		1: {PredictedMS: 4_000, SampleCount: 5, UpdatedAt: now},
		2: {PredictedMS: 5_000, SampleCount: 5, UpdatedAt: now},
		3: {PredictedMS: 6_000, SampleCount: 5, UpdatedAt: now},
	}}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	normal := &Group{ID: 10, Name: "PLUS分组", Platform: PlatformOpenAI, Status: StatusActive}
	hidden := &Group{ID: 84, Name: firstTokenLatencyHiddenGroup, Platform: PlatformOpenAI, Status: StatusActive}
	accounts := []Account{
		{
			ID: 1, Name: firstTokenLatencyHiddenAccount, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{normal.ID}, Groups: []*Group{normal},
			AccountGroups: []AccountGroup{{AccountID: 1, GroupID: normal.ID, Group: normal}},
		},
		{
			ID: 2, Name: "monitor-only", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{hidden.ID}, Groups: []*Group{hidden},
			AccountGroups: []AccountGroup{{AccountID: 2, GroupID: hidden.ID, Group: hidden}},
		},
		{
			ID: 3, Name: "shared", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{normal.ID, hidden.ID}, Groups: []*Group{normal, hidden},
			AccountGroups: []AccountGroup{
				{AccountID: 3, GroupID: normal.ID, Group: normal},
				{AccountID: 3, GroupID: hidden.ID, Group: hidden},
			},
		},
	}

	metrics, err := svc.AccountFirstTokenLatencyMetrics(context.Background(), accounts)
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, int64(3), metrics[0].AccountID)
	require.Equal(t, []AccountFirstTokenLatencyGroup{{GroupID: normal.ID, GroupName: normal.Name}}, metrics[0].Groups)

	durationMS := 4200
	svc.ObserveTotalDurationLatency(context.Background(), &accounts[0], &UsageLog{
		RequestID: "hidden-dashboard-account", RequestType: RequestTypeStream, Stream: true, ActualCost: 0.01, DurationMs: &durationMS,
	})
	require.Equal(t, accounts[0].ID, cache.recordedID, "dashboard-only exclusion must not disable sampling")
}
