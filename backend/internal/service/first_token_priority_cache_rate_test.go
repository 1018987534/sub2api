package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestFirstTokenPriorityAllFastCacheRateOrder(t *testing.T) {
	tests := []struct {
		name         string
		stats        map[int64]AccountCacheStats
		providerErr  error
		slowID       int64
		staleID      int64
		unmeasuredID int64
		want         []int64
	}{
		{
			name: "higher cache above eighty beats cheaper upstream rate",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 50, CacheRateDenominator: 100},
				2: {CacheReadTokens: 90, CacheRateDenominator: 100},
				3: {CacheReadTokens: 95, CacheRateDenominator: 100},
			},
			want: []int64{3, 2, 1},
		},
		{
			name: "at or below eighty retains upstream rate order",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 50, CacheRateDenominator: 100},
				2: {CacheReadTokens: 80, CacheRateDenominator: 100},
				3: {CacheReadTokens: 79, CacheRateDenominator: 100},
			},
			want: []int64{1, 2, 3},
		},
		{
			name: "only strictly above eighty is promoted",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 80, CacheRateDenominator: 100},
				2: {CacheReadTokens: 80001, CacheRateDenominator: 100000},
				3: {CacheReadTokens: 80, CacheRateDenominator: 100},
			},
			want: []int64{2, 1, 3},
		},
		{
			name: "equal high cache retains upstream rate order",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 90, CacheRateDenominator: 100},
				2: {CacheReadTokens: 900, CacheRateDenominator: 1000},
				3: {CacheReadTokens: 90, CacheRateDenominator: 100},
			},
			want: []int64{1, 2, 3},
		},
		{
			name: "lower cache tail still uses upstream rate",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 5, CacheRateDenominator: 100},
				2: {CacheReadTokens: 79, CacheRateDenominator: 100},
				3: {CacheReadTokens: 95, CacheRateDenominator: 100},
			},
			want: []int64{3, 1, 2},
		},
		{
			name: "uncalculable rate is not treated as one hundred percent",
			stats: map[int64]AccountCacheStats{
				2: {CacheReadTokens: 90, CacheRateDenominator: 100},
				3: {CacheReadTokens: 100, CacheRateDenominator: 0},
			},
			want: []int64{2, 1, 3},
		},
		{name: "missing statistics retain upstream rate order", want: []int64{1, 2, 3}},
		{
			name:        "provider failure retains upstream rate order",
			providerErr: errors.New("cache statistics unavailable"),
			want:        []int64{1, 2, 3},
		},
		{
			name: "mixed pools rank fast accounts by cache and retain slow fallback",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 50, CacheRateDenominator: 100},
				2: {CacheReadTokens: 95, CacheRateDenominator: 100},
				3: {CacheReadTokens: 99, CacheRateDenominator: 100},
			},
			slowID: 3,
			want:   []int64{2, 1, 3},
		},
		{
			name: "stale candidate does not disable fast pool cache priority",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 50, CacheRateDenominator: 100},
				2: {CacheReadTokens: 95, CacheRateDenominator: 100},
				3: {CacheReadTokens: 99, CacheRateDenominator: 100},
			},
			staleID: 3,
			want:    []int64{2, 1, 3},
		},
		{
			name: "unmeasured candidate cannot outrank confirmed fast accounts",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 50, CacheRateDenominator: 100},
				2: {CacheReadTokens: 95, CacheRateDenominator: 100},
				3: {CacheReadTokens: 99, CacheRateDenominator: 100},
			},
			unmeasuredID: 3,
			want:         []int64{2, 1, 3},
		},
		{
			name: "mixed pools below eighty still rank fast accounts by upstream rate",
			stats: map[int64]AccountCacheStats{
				1: {CacheReadTokens: 50, CacheRateDenominator: 100},
				2: {CacheReadTokens: 80, CacheRateDenominator: 100},
				3: {CacheReadTokens: 99, CacheRateDenominator: 100},
			},
			slowID: 3,
			want:   []int64{1, 2, 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			accounts := make([]*Account, 0, 3)
			latency := make(map[int64]FirstTokenLatencyStats)
			for i, rate := range []float64{0.05, 0.10, 0.50} {
				account := upstreamCostTestAccount(int64(i+1), UpstreamBillingProbeStatusOK, rate, now.Add(-time.Minute), 30*time.Minute)
				account.Status, account.Schedulable = StatusActive, true
				accounts = append(accounts, account)
				duration := 5000.0
				if account.ID == tt.slowID {
					duration = 35000
				}
				latency[account.ID] = FirstTokenLatencyStats{PredictedMS: duration, SampleCount: 20, UpdatedAt: now}
				if account.ID == tt.staleID {
					latency[account.ID] = FirstTokenLatencyStats{PredictedMS: duration, SampleCount: 20, UpdatedAt: now.Add(-7 * time.Hour)}
				}
				if account.ID == tt.unmeasuredID {
					latency[account.ID] = FirstTokenLatencyStats{SampleCount: 8, UpdatedAt: now, FastConfirmationTracked: true}
				}
			}
			cache := &staticFirstTokenLatencyStatsCache{stats: latency}
			service := &RateLimitService{usageRepo: cacheRateProviderStub{stats: tt.stats, err: tt.providerErr}}
			order := []openAIAccountCandidateScore{{account: accounts[2]}, {account: accounts[1]}, {account: accounts[0]}}
			got := applyOpenAIFirstTokenPriorityOrder(context.Background(), OpenAIAccountScheduleRequest{}, order, cache, floatPtr(defaultOpenAIOAuthSchedulingRateMultiplier), service)
			require.Equal(t, tt.want, candidateAccountIDs(got), "OpenAI scheduler")
			ranks := firstTokenPriorityRanksWithProbeOptions(context.Background(), accounts, cache, false, false, service)
			for rank, id := range tt.want {
				require.Equal(t, rank, ranks[id], "gateway scheduler account %d", id)
			}
		})
	}
}

func TestFastPoolCacheRateSchedulingChoosesHighestCache(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	now := time.Now()
	groupID := int64(48200)
	accounts := make([]Account, 0, 3)
	latency := make(map[int64]FirstTokenLatencyStats)
	cacheStats := make(map[int64]AccountCacheStats)
	for i, rate := range []float64{0.05, 0.10, 0.50} {
		account := upstreamCostTestAccount(int64(i+1), UpstreamBillingProbeStatusOK, rate, now.Add(-time.Minute), 30*time.Minute)
		account.Status, account.Schedulable = StatusActive, true
		account.Concurrency = 1
		account.GroupIDs = []int64{groupID}
		accounts = append(accounts, *account)
		latency[account.ID] = FirstTokenLatencyStats{PredictedMS: 5000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true}
		cacheStats[account.ID] = AccountCacheStats{CacheReadTokens: []int64{50, 90, 95}[i], CacheRateDenominator: 100}
	}
	repo := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyFirstTokenPriorityEnabled: "true"}}
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       &schedulerTestGatewayCache{},
		cfg:         &config.Config{},
		rateLimitService: &RateLimitService{
			settingService:              NewSettingService(repo, &config.Config{}),
			firstTokenLatencyStatsCache: &staticFirstTokenLatencyStatsCache{stats: latency},
			usageRepo:                   cacheRateProviderStub{stats: cacheStats},
		},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	selection, decision, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	require.Equal(t, int64(3), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
}

func TestFastPoolCacheNewRequestChoosesHighestCacheWithFallback(t *testing.T) {
	for _, fallback := range []string{"slow", "stale", "insufficient", "missing"} {
		t.Run(fallback, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			now := time.Now()
			groupID := int64(48201)
			accounts := make([]Account, 0, 3)
			latency := make(map[int64]FirstTokenLatencyStats)
			cacheStats := make(map[int64]AccountCacheStats)
			for i, rate := range []float64{0.05, 0.15, 0.10} {
				account := upstreamCostTestAccount(int64(i+1), UpstreamBillingProbeStatusOK, rate, now.Add(-time.Minute), 30*time.Minute)
				account.Status, account.Schedulable = StatusActive, true
				account.Concurrency, account.GroupIDs = 100, []int64{groupID}
				accounts = append(accounts, *account)
				latency[account.ID] = FirstTokenLatencyStats{PredictedMS: 5000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true}
				cacheStats[account.ID] = AccountCacheStats{CacheReadTokens: []int64{50, 95, 99}[i], CacheRateDenominator: 100}
			}
			switch fallback {
			case "slow":
				latency[3] = FirstTokenLatencyStats{PredictedMS: 35000, SampleCount: 20, UpdatedAt: now, FastConfirmationTracked: true}
			case "stale":
				stat := latency[3]
				stat.UpdatedAt = now.Add(-7 * time.Hour)
				latency[3] = stat
			case "insufficient":
				latency[3] = FirstTokenLatencyStats{SampleCount: 8, UpdatedAt: now, FastConfirmationTracked: true}
			case "missing":
				delete(latency, 3)
			}
			repo := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyFirstTokenPriorityEnabled: "true"}}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:       &schedulerTestGatewayCache{},
				cfg:         &config.Config{},
				rateLimitService: &RateLimitService{
					settingService:              NewSettingService(repo, &config.Config{}),
					firstTokenLatencyStatsCache: &staticFirstTokenLatencyStatsCache{stats: latency},
					usageRepo:                   cacheRateProviderStub{stats: cacheStats},
				},
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			selection, decision, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			if selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			require.Equal(t, int64(2), selection.Account.ID)
			require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
			require.False(t, decision.StickySessionHit)
		})
	}
}
