package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fastPoolPrioritySettingsRepo struct {
	openAIAdvancedSchedulerSettingRepoStub
	getErr error
	setErr error
	writes int
}

func (r *fastPoolPrioritySettingsRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.openAIAdvancedSchedulerSettingRepoStub.GetMultiple(ctx, keys)
}

func (r *fastPoolPrioritySettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range values {
		r.values[key] = value
	}
	r.writes++
	return nil
}

func TestFastPoolPrioritySettingsDefaultsValidationAndReadFailures(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolPrioritySettingsRepo{}
	svc := NewSettingService(repo, nil)
	for _, value := range []string{"", "  ", "intelligence", " latency ", " rate "} {
		repo.values = map[string]string{SettingKeyFastPoolPriorityMode: value}
		got, err := svc.GetFastPoolPrioritySettings(ctx)
		require.NoError(t, err)
		want := FastPoolPriorityIntelligence
		if value == " latency " {
			want = FastPoolPriorityLatency
		} else if value == " rate " {
			want = FastPoolPriorityRate
		}
		require.Equal(t, want, got.Mode)
		require.Equal(t, FastPoolPriorityIntelligence, got.BelowThresholdMode)
		require.Equal(t, floatPtr(80), got.CacheRateThresholdPercent)
	}
	repo.values = nil
	got, err := svc.GetFastPoolPrioritySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, FastPoolPriorityIntelligence, got.Mode)
	require.Equal(t, floatPtr(80), got.CacheRateThresholdPercent)
	for _, value := range []string{"cache", "RATE", "0"} {
		repo.values = map[string]string{SettingKeyFastPoolPriorityMode: value}
		_, err := svc.GetFastPoolPrioritySettings(ctx)
		require.Error(t, err)
		require.Equal(t, FastPoolPriorityIntelligence, (&RateLimitService{settingService: svc}).fastPoolPrioritySettings(ctx).Mode)
	}
	repo.getErr = errors.New("storage unavailable")
	_, err = svc.GetFastPoolPrioritySettings(ctx)
	require.Error(t, err)
	require.Equal(t, FastPoolPriorityIntelligence, (&RateLimitService{settingService: svc}).fastPoolPrioritySettings(ctx).Mode)
}

func TestFastPoolPrioritySettingsPersistsAcrossRunningInstances(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolPrioritySettingsRepo{}
	control := NewSettingService(repo, nil)
	gateway := NewSettingService(repo, nil)
	for _, mode := range []FastPoolPriorityMode{FastPoolPriorityRate, FastPoolPriorityLatency, FastPoolPriorityIntelligence} {
		saved, err := control.UpdateFastPoolPrioritySettings(ctx, mode, nil, FastPoolPriorityIntelligence)
		require.NoError(t, err)
		require.Equal(t, mode, saved.Mode)
		readback, err := gateway.GetFastPoolPrioritySettings(ctx)
		require.NoError(t, err)
		require.Equal(t, saved, readback, "an already-running gateway must read the saved mode")
		reloaded, err := NewSettingService(repo, nil).GetFastPoolPrioritySettings(ctx)
		require.NoError(t, err)
		require.Equal(t, saved, reloaded)
	}
	for _, mode := range []FastPoolPriorityMode{"", "cache", " rate"} {
		_, err := control.UpdateFastPoolPrioritySettings(ctx, mode, nil, FastPoolPriorityIntelligence)
		require.Error(t, err)
	}
	require.Equal(t, 3, repo.writes)
	repo.setErr = errors.New("write failed")
	_, err := control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate, nil, FastPoolPriorityIntelligence)
	require.Error(t, err)
	require.Equal(t, string(FastPoolPriorityIntelligence), repo.values[SettingKeyFastPoolPriorityMode])
}

func TestFastPoolPriorityThresholdPersistenceValidationAndClear(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolPrioritySettingsRepo{}
	control, gateway := NewSettingService(repo, nil), NewSettingService(repo, nil)
	for _, threshold := range []*float64{floatPtr(0), floatPtr(80), floatPtr(80.25), floatPtr(100), nil} {
		saved, err := control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate, threshold, FastPoolPriorityIntelligence)
		require.NoError(t, err)
		readback, err := gateway.GetFastPoolPrioritySettings(ctx)
		require.NoError(t, err)
		require.Equal(t, saved, readback)
		require.Equal(t, threshold, readback.CacheRateThresholdPercent)
	}
	require.Empty(t, repo.values[SettingKeyFastPoolPriorityCacheRateThresholdPercent])
	writes := repo.writes
	for _, invalid := range []float64{-1, 100.01, math.NaN(), math.Inf(1)} {
		_, err := control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate, &invalid, FastPoolPriorityIntelligence)
		require.Error(t, err)
	}
	require.Equal(t, writes, repo.writes)
	for _, stored := range []string{"-1", "101", "NaN", "+Inf", "bad"} {
		repo.values[SettingKeyFastPoolPriorityCacheRateThresholdPercent] = stored
		_, err := gateway.GetFastPoolPrioritySettings(ctx)
		require.Error(t, err)
		require.Equal(t, floatPtr(80), (&RateLimitService{settingService: gateway}).fastPoolPrioritySettings(ctx).CacheRateThresholdPercent)
	}
}

func TestFastPoolPrioritySavedModeChangesNewSelectionsAndKeepsSlowTail(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	now := time.Now()
	groupID := int64(48202)
	accounts := make([]Account, 0, 4)
	latency := make(map[int64]FirstTokenLatencyStats)
	cacheStats := make(map[int64]AccountCacheStats)
	for i, rate := range []float64{0.05, 0.10, 0.50, 0.01} {
		account := upstreamCostTestAccount(int64(i+1), UpstreamBillingProbeStatusOK, rate, now.Add(-time.Minute), 30*time.Minute)
		account.Status, account.Schedulable = StatusActive, true
		account.Concurrency, account.GroupIDs = 100, []int64{groupID}
		accounts = append(accounts, *account)
		latency[account.ID] = FirstTokenLatencyStats{PredictedMS: 5000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true}
		cacheStats[account.ID] = AccountCacheStats{CacheReadTokens: []int64{50, 90, 95, 99}[i], CacheRateDenominator: 100}
	}
	latency[4] = FirstTokenLatencyStats{PredictedMS: 35000, SampleCount: 20, UpdatedAt: now, FastConfirmationTracked: true}
	latencyOne := latency[1]
	latencyOne.PredictedMS = 9000
	latency[1] = latencyOne
	latencyTwo := latency[2]
	latencyTwo.PredictedMS = 6000
	latency[2] = latencyTwo
	latencyThree := latency[3]
	latencyThree.PredictedMS = 9000
	latency[3] = latencyThree
	repo := &fastPoolPrioritySettingsRepo{openAIAdvancedSchedulerSettingRepoStub: openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyFirstTokenPriorityEnabled: "true"}}}
	control := NewSettingService(repo, nil)
	gateways := make([]*OpenAIGatewayService, 2)
	for i := range gateways {
		gateways[i] = &OpenAIGatewayService{
			accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
			cache:       &schedulerTestGatewayCache{}, cfg: &config.Config{},
			rateLimitService: &RateLimitService{
				settingService:              NewSettingService(repo, nil),
				firstTokenLatencyStatsCache: &staticFirstTokenLatencyStatsCache{stats: latency},
				usageRepo:                   cacheRateProviderStub{stats: cacheStats},
			},
			concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
		}
	}
	for _, scenario := range []struct {
		mode      FastPoolPriorityMode
		threshold *float64
		want      []int64
		belowMode FastPoolPriorityMode
	}{
		{FastPoolPriorityIntelligence, nil, []int64{3, 2, 1, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityLatency, nil, []int64{2, 1, 3, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityRate, nil, []int64{1, 2, 3, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityRate, floatPtr(90), []int64{2, 3, 1, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityRate, floatPtr(95), []int64{3, 2, 1, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityLatency, floatPtr(90), []int64{2, 3, 1, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityRate, nil, []int64{1, 2, 3, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityIntelligence, nil, []int64{3, 2, 1, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityRate, floatPtr(100), []int64{3, 2, 1, 4}, FastPoolPriorityIntelligence},
		{FastPoolPriorityRate, floatPtr(100), []int64{1, 2, 3, 4}, FastPoolPriorityRate},
		{FastPoolPriorityRate, floatPtr(100), []int64{2, 1, 3, 4}, FastPoolPriorityLatency},
		{FastPoolPriorityLatency, floatPtr(95), []int64{3, 1, 2, 4}, FastPoolPriorityRate},
	} {
		_, err := control.UpdateFastPoolPrioritySettings(ctx, scenario.mode, scenario.threshold, scenario.belowMode)
		require.NoError(t, err)
		for _, gateway := range gateways {
			selection, _, err := gateway.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, scenario.want[0], selection.Account.ID, "new selection must follow saved %s mode", scenario.mode)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			candidates := []openAIAccountCandidateScore{{account: &accounts[3]}, {account: &accounts[2]}, {account: &accounts[1]}, {account: &accounts[0]}}
			got := applyOpenAIFirstTokenPriorityOrder(ctx, OpenAIAccountScheduleRequest{}, candidates, gateway.rateLimitService.firstTokenLatencyStatsCache, floatPtr(defaultOpenAIOAuthSchedulingRateMultiplier), gateway.rateLimitService)
			require.Equal(t, scenario.want, candidateAccountIDs(got))
			// The generic gateway uses the same rank entry point after rate ordering.
			ordered := []*Account{&accounts[3], &accounts[0], &accounts[1], &accounts[2]}
			ranks := firstTokenPriorityRanksWithProbeOptions(ctx, ordered, gateway.rateLimitService.firstTokenLatencyStatsCache, false, false, gateway.rateLimitService)
			for rank, id := range scenario.want {
				require.Equal(t, rank, ranks[id], "generic gateway rank in %s mode", scenario.mode)
			}
		}
	}
}

func TestFastPoolPriorityOptionalThresholdAndModeOrder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      FastPoolPriorityMode
		threshold *float64
		missing   bool
		err       error
		want      []int64
	}{
		{"unset uses rate only", FastPoolPriorityRate, nil, false, nil, []int64{1, 2, 3}},
		{"rate includes exact threshold", FastPoolPriorityRate, floatPtr(80), false, nil, []int64{2, 3, 1}},
		{"rate threshold can be raised", FastPoolPriorityRate, floatPtr(90), false, nil, []int64{3, 2, 1}},
		{"all below threshold default to cache", FastPoolPriorityRate, floatPtr(100), false, nil, []int64{3, 2, 1}},
		{"zero is configured", FastPoolPriorityRate, floatPtr(0), true, nil, []int64{2, 3, 1}},
		{"unset uses latency only", FastPoolPriorityLatency, nil, false, nil, []int64{1, 3, 2}},
		{"latency prioritizes exact threshold then duration", FastPoolPriorityLatency, floatPtr(80), false, nil, []int64{3, 2, 1}},
		{"latency applies only above threshold", FastPoolPriorityLatency, floatPtr(90), false, nil, []int64{3, 2, 1}},
		{"cache mode sorts all measured rates", FastPoolPriorityIntelligence, nil, false, nil, []int64{3, 2, 1}},
		{"unknown samples never qualify", FastPoolPriorityRate, floatPtr(80), true, nil, []int64{2, 3, 1}},
		{"read failure retains rate", FastPoolPriorityRate, floatPtr(80), false, errors.New("unavailable"), []int64{1, 2, 3}},
		{"read failure leaves unknown low tier stable", FastPoolPriorityLatency, floatPtr(80), false, errors.New("unavailable"), []int64{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := []int64{1, 2, 3}
			latency := map[int64]FirstTokenLatencyStats{1: {PredictedMS: 1000}, 2: {PredictedMS: 9000}, 3: {PredictedMS: 6000}}
			rates := map[int64]AccountCacheStats{1: {CacheReadTokens: 79999, CacheRateDenominator: 100000}, 2: {CacheReadTokens: 80, CacheRateDenominator: 100}, 3: {CacheReadTokens: 95, CacheRateDenominator: 100}}
			if tc.missing {
				delete(rates, 1)
			}
			svc := &RateLimitService{usageRepo: cacheRateProviderStub{stats: rates, err: tc.err}}
			orderFastPool(context.Background(), ids, latency, &FastPoolPrioritySettings{Mode: tc.mode, CacheRateThresholdPercent: tc.threshold}, svc)
			require.Equal(t, tc.want, ids)
		})
	}
}

func TestFastPoolBelowThresholdSettingsPersistenceAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolPrioritySettingsRepo{}
	control, gateway := NewSettingService(repo, nil), NewSettingService(repo, nil)
	for _, mode := range []FastPoolPriorityMode{FastPoolPriorityRate, FastPoolPriorityLatency, FastPoolPriorityIntelligence} {
		saved, err := control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate, floatPtr(80), mode)
		require.NoError(t, err)
		readback, err := gateway.GetFastPoolPrioritySettings(ctx)
		require.NoError(t, err)
		require.Equal(t, saved, readback)
		require.Equal(t, mode, readback.BelowThresholdMode)
	}
	writes := repo.writes
	for _, mode := range []FastPoolPriorityMode{"", "cache", " rate"} {
		_, err := control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate, floatPtr(80), mode)
		require.Error(t, err)
	}
	require.Equal(t, writes, repo.writes)
	repo.values[SettingKeyFastPoolPriorityBelowThresholdMode] = "invalid"
	_, err := gateway.GetFastPoolPrioritySettings(ctx)
	require.Error(t, err)
	require.Equal(t, FastPoolPriorityIntelligence, (&RateLimitService{settingService: gateway}).fastPoolPrioritySettings(ctx).BelowThresholdMode)
}

func TestFastPoolIndependentTierStrategies(t *testing.T) {
	for _, high := range []FastPoolPriorityMode{FastPoolPriorityRate, FastPoolPriorityLatency, FastPoolPriorityIntelligence} {
		for _, low := range []FastPoolPriorityMode{FastPoolPriorityRate, FastPoolPriorityLatency, FastPoolPriorityIntelligence} {
			t.Run(string(high)+"/"+string(low), func(t *testing.T) {
				ids := []int64{1, 2, 3, 4, 5}
				rates := map[int64]AccountCacheStats{}
				latency := map[int64]FirstTokenLatencyStats{}
				for i, rate := range []int64{80, 90, 60, 75, 65} {
					id := int64(i + 1)
					rates[id] = AccountCacheStats{CacheReadTokens: rate, CacheRateDenominator: 100}
					latency[id] = FirstTokenLatencyStats{PredictedMS: float64(6000 - i*1000)}
				}
				svc := &RateLimitService{usageRepo: cacheRateProviderStub{stats: rates}}
				orderFastPool(context.Background(), ids, latency, &FastPoolPrioritySettings{Mode: high, BelowThresholdMode: low, CacheRateThresholdPercent: floatPtr(80)}, svc)
				upper := []int64{1, 2}
				if high != FastPoolPriorityRate {
					upper = []int64{2, 1}
				}
				lower := map[FastPoolPriorityMode][]int64{FastPoolPriorityRate: {3, 4, 5}, FastPoolPriorityLatency: {5, 4, 3}, FastPoolPriorityIntelligence: {4, 5, 3}}[low]
				require.Equal(t, append(upper, lower...), ids)
			})
		}
	}
}
