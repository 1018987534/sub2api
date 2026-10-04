package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type intelligenceProtectionRepo struct {
	schedulerTestOpenAIAccountRepo
	pausedID int64
	until    time.Time
	groups   []Group
}

func (r *intelligenceProtectionRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, _ string) error {
	r.pausedID, r.until = id, until
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].TempUnschedulableUntil = &until
		}
	}
	return nil
}

func (r *intelligenceProtectionRepo) GetGroups(context.Context, int64) ([]Group, error) {
	return r.groups, nil
}
func (r *intelligenceProtectionRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			if r.accounts[i].Extra == nil {
				r.accounts[i].Extra = map[string]any{}
			}
			for k, v := range updates {
				r.accounts[i].Extra[k] = v
			}
			r.pausedID = id
			r.until, _ = time.Parse(time.RFC3339Nano, r.accounts[i].GetExtraString(intelligencePauseUntilKey))
		}
	}
	return nil
}

func (r *intelligenceProtectionRepo) ClearIntelligenceTempUnschedulable(_ context.Context, id int64, reason string) error {
	for i := range r.accounts {
		if r.accounts[i].ID == id && r.accounts[i].TempUnschedulableReason == reason {
			r.accounts[i].TempUnschedulableUntil = nil
			r.accounts[i].TempUnschedulableReason = ""
		}
	}
	return nil
}

type intelligenceResetCache struct {
	staticFirstTokenLatencyStatsCache
	resetID int64
}

func (c *intelligenceResetCache) ResetForIntelligencePause(_ context.Context, id int64, _ time.Time) error {
	c.resetID = id
	return nil
}

func (c *intelligenceResetCache) ClearIntelligencePause(context.Context, int64) error { return nil }

func TestIntelligenceAccountProtection(t *testing.T) {
	for _, scenario := range []string{"two", "only", "disabled", "cooldown", "model", "cache_rate", "profit", "ordinary_group", "pool_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil))
			t.Cleanup(func() { openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil)) })
			group := &Group{ID: 7, Name: "013不降智", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1}
			a, b := cacheRateTestAccount(1), cacheRateTestAccount(2)
			enabled := "true"
			provider := cacheRateProviderStub{stats: map[int64]AccountCacheStats{}}
			switch scenario {
			case "disabled":
				b.Schedulable = false
			case "cooldown":
				until := time.Now().Add(time.Hour)
				b.TempUnschedulableUntil = &until
			case "model":
				b.Credentials = map[string]any{"model_mapping": map[string]any{"different-model": "different-model"}}
			case "cache_rate":
				group.MinCacheRate = .8
				provider.stats[2] = AccountCacheStats{CacheReadTokens: 20, CacheRateDenominator: 100}
			case "profit":
				group.ProfitControlEnabled = true
				group.ProfitMinMargin = .5
				rate := .9
				b.RateMultiplier = &rate
			case "ordinary_group":
				group.Name = "特价"
			case "pool_disabled":
				enabled = "false"
			}
			accounts := []Account{*a, *b}
			if scenario == "only" {
				accounts = accounts[:1]
			}
			repo := &intelligenceProtectionRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, groups: []Group{*group}}
			cache := &intelligenceResetCache{}
			settings := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyTotalDurationPriorityEnabled: enabled}}
			svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{firstTokenLatencyStatsCache: cache, usageRepo: provider, settingService: &SettingService{settingRepo: settings}}}
			start := time.Now()
			paused, err := svc.ProtectDegradedIntelligenceAccount(context.Background(), group, 1, "gpt-6.1-sol", 0)
			require.NoError(t, err)
			if scenario == "two" {
				require.True(t, paused)
				require.Equal(t, int64(1), repo.pausedID)
				require.Equal(t, int64(1), cache.resetID)
				require.WithinDuration(t, start.Add(20*time.Minute), repo.until, time.Second)
				// The second degraded account is now the last usable one.
				paused, err = svc.ProtectDegradedIntelligenceAccount(context.Background(), group, 2, "gpt-6.1-sol", 0)
				require.NoError(t, err)
				require.False(t, paused)
				require.Equal(t, int64(1), repo.pausedID)
				t.Log("two accounts: first paused=20m pending=true; remaining account: unchanged")
			} else {
				require.False(t, paused)
				require.Zero(t, repo.pausedID)
				require.Zero(t, cache.resetID)
				t.Log("account and pool unchanged")
			}
		})
	}
}

func TestIntelligencePausedAccountRemainsVisiblePending(t *testing.T) {
	until := time.Now().Add(20 * time.Minute)
	account := cacheRateTestAccount(1)
	account.TempUnschedulableUntil = &until
	account.TempUnschedulableReason = "intelligence: consecutive degraded"
	cache := &staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{1: {CircuitBroken: true, UpdatedAt: time.Now()}}}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	metrics, err := svc.AccountFirstTokenLatencyMetrics(context.Background(), []Account{*account})
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.False(t, metrics[0].IsFastPool)
	require.False(t, metrics[0].HasPrediction)
	require.True(t, metrics[0].CircuitBroken)
	require.False(t, account.IsSchedulable())
}
