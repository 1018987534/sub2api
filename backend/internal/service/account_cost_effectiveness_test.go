package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestEffectiveInputRateMultiplier(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rate  *float64
		cache AccountCacheStats
		want  *float64
	}{
		{"cheap upstream with 80 percent cache", floatPtr(.03), AccountCacheStats{80, 100}, floatPtr(.0084)},
		{"more cache can still cost more", floatPtr(.1), AccountCacheStats{95, 100}, floatPtr(.0145)},
		{"100 percent cache is not free", floatPtr(.1), AccountCacheStats{100, 100}, floatPtr(.01)},
		{"zero cache", floatPtr(.03), AccountCacheStats{0, 100}, floatPtr(.03)},
		{"explicit zero upstream rate", floatPtr(0), AccountCacheStats{80, 100}, floatPtr(0)},
		{"no samples is not 100 percent measured", floatPtr(.03), AccountCacheStats{}, nil},
		{"missing fresh upstream rate", nil, AccountCacheStats{80, 100}, nil},
		{"invalid cache totals", floatPtr(.03), AccountCacheStats{101, 100}, nil},
		{"negative cache", floatPtr(.03), AccountCacheStats{-1, 100}, nil},
		{"invalid multiplier", floatPtr(math.NaN()), AccountCacheStats{80, 100}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveInputRateMultiplier(tc.rate, tc.cache)
			if tc.want == nil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.InDelta(t, *tc.want, *got, 1e-12)
		})
	}
}

func TestCostEffectivenessGroupSelectKeepsFastPoolAndOtherGroups(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	ctx, now := context.Background(), time.Now()
	target := Group{ID: 103, Name: "测试不降智", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: .001}
	other := Group{ID: 999, Name: "其他不降智", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1}
	accounts := make([]Account, 0, 4)
	latency, cacheStats := map[int64]FirstTokenLatencyStats{}, map[int64]AccountCacheStats{}
	for index, rate := range []float64{.03, .10, .001, .001} {
		a := upstreamCostTestAccount(int64(index+1), UpstreamBillingProbeStatusOK, rate, now.Add(-time.Minute), 30*time.Minute)
		a.Status, a.Schedulable, a.Concurrency = StatusActive, true, 100
		a.GroupIDs, a.Groups = []int64{target.ID, other.ID}, []*Group{&target, &other}
		accounts = append(accounts, *a)
		latency[a.ID] = FirstTokenLatencyStats{PredictedMS: 5000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true}
		cacheStats[a.ID] = AccountCacheStats{CacheReadTokens: []int64{80, 95, 99, 100}[index], CacheRateDenominator: 100}
	}
	latency[3] = FirstTokenLatencyStats{PredictedMS: 40000, SampleCount: 20, UpdatedAt: now, FastConfirmationTracked: true}
	accounts[3].Extra[intelligencePauseUntilKey] = now.Add(time.Hour).Format(time.RFC3339Nano)
	accounts[3].Extra[intelligenceRecoveryRequiredKey] = true
	accounts[3].Extra[intelligenceProtectedGroupsKey] = []int64{other.ID}
	repo := &fastPoolPrioritySettingsRepo{openAIAdvancedSchedulerSettingRepoStub: openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyFirstTokenPriorityEnabled: "true"}}}
	control := NewSettingService(repo, nil)
	_, err := control.UpdateFastPoolCostEffectivenessGroup(ctx, target.ID, true)
	require.NoError(t, err)
	// The target ignores global cache tiers, while the control group retains them.
	_, err = control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate, floatPtr(95), FastPoolPriorityLatency)
	require.NoError(t, err)
	gateway := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{}, cfg: &config.Config{},
		rateLimitService:   &RateLimitService{settingService: NewSettingService(repo, nil), firstTokenLatencyStatsCache: &staticFirstTokenLatencyStatsCache{stats: latency}, usageRepo: cacheRateProviderStub{stats: cacheStats}},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	for _, scenario := range []struct {
		group int64
		want  int64
	}{{target.ID, 1}, {other.ID, 2}} {
		selected, _, err := gateway.SelectAccountWithScheduler(ctx, &scenario.group, "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.Equal(t, scenario.want, selected.Account.ID)
		if selected.ReleaseFunc != nil {
			selected.ReleaseFunc()
		}
	}
	metrics, err := gateway.rateLimitService.AccountFirstTokenLatencyMetrics(ctx, accounts)
	require.NoError(t, err)
	for _, m := range metrics {
		if m.AccountID == 1 {
			require.InDelta(t, .0084, *m.EffectiveInputRateMultiplier, 1e-12)
		}
		if m.AccountID == 4 {
			for _, g := range m.Groups {
				if g.GroupID == target.ID {
					require.True(t, g.IntelligenceBlocked)
					require.False(t, g.IntelligenceExempt)
				}
			}
		}
	}
	// Saving a global strategy from an older client preserves the opt-in group.
	settings, err := control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate, nil, FastPoolPriorityLatency)
	require.NoError(t, err)
	require.True(t, settings.CostEffectivenessEnabled(target.ID))
	settings, err = control.UpdateFastPoolCostEffectivenessGroup(ctx, target.ID, false)
	require.NoError(t, err)
	require.False(t, settings.CostEffectivenessEnabled(target.ID))
	require.Equal(t, FastPoolPriorityRate, settings.Mode)
	require.Equal(t, FastPoolPriorityLatency, settings.BelowThresholdMode)
}

func TestCostEffectivenessSoleAccountMustRemainPausedUntilRecovery(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	group := Group{ID: 103, Name: "测试不降智", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: .001}
	a := cacheRateTestAccount(1)
	a.GroupIDs, a.Groups = []int64{group.ID}, []*Group{&group}
	repo := &intelligenceProtectionRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*a}}, groups: []Group{group}}
	settings := &fastPoolPrioritySettingsRepo{openAIAdvancedSchedulerSettingRepoStub: openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyTotalDurationPriorityEnabled: "true", SettingKeyFastPoolCostEffectivenessGroupIDs: "[103]"}}}
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{firstTokenLatencyStatsCache: &intelligenceResetCache{}, settingService: NewSettingService(settings, nil)}}
	paused, err := svc.ProtectDegradedIntelligenceAccount(context.Background(), &group, 1, "gpt-6.1-sol", 0)
	require.NoError(t, err)
	require.True(t, paused)
	account, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, svc.intelligenceAccountBlockedForGroup(context.Background(), account, &group.ID, time.Now()))
	require.Empty(t, intelligenceAllowedGroups(account))
	require.NoError(t, svc.RecoverIntelligenceAccount(context.Background(), 1))
	require.False(t, svc.intelligenceAccountBlockedForGroup(context.Background(), account, &group.ID, time.Now()))
}
