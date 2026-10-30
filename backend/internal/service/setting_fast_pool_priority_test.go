package service

import (
	"context"
	"errors"
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

func (r *fastPoolPrioritySettingsRepo) GetValue(ctx context.Context, key string) (string, error) {
	if key == SettingKeyFastPoolPriorityMode && r.getErr != nil {
		return "", r.getErr
	}
	return r.openAIAdvancedSchedulerSettingRepoStub.GetValue(ctx, key)
}

func (r *fastPoolPrioritySettingsRepo) Set(_ context.Context, key, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	r.writes++
	return nil
}

func TestFastPoolPrioritySettingsDefaultsValidationAndReadFailures(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolPrioritySettingsRepo{}
	svc := NewSettingService(repo, nil)
	for _, value := range []string{"", "  ", "intelligence", " rate "} {
		repo.values = map[string]string{SettingKeyFastPoolPriorityMode: value}
		got, err := svc.GetFastPoolPrioritySettings(ctx)
		require.NoError(t, err)
		want := FastPoolPriorityIntelligence
		if value == " rate " {
			want = FastPoolPriorityRate
		}
		require.Equal(t, want, got.Mode)
	}
	repo.values = nil
	got, err := svc.GetFastPoolPrioritySettings(ctx)
	require.NoError(t, err)
	require.Equal(t, FastPoolPriorityIntelligence, got.Mode)
	for _, value := range []string{"cache", "RATE", "0"} {
		repo.values = map[string]string{SettingKeyFastPoolPriorityMode: value}
		_, err := svc.GetFastPoolPrioritySettings(ctx)
		require.Error(t, err)
		require.Equal(t, FastPoolPriorityIntelligence, (&RateLimitService{settingService: svc}).fastPoolPriorityMode(ctx))
	}
	repo.getErr = errors.New("storage unavailable")
	_, err = svc.GetFastPoolPrioritySettings(ctx)
	require.Error(t, err)
	require.Equal(t, FastPoolPriorityIntelligence, (&RateLimitService{settingService: svc}).fastPoolPriorityMode(ctx))
}

func TestFastPoolPrioritySettingsPersistsAcrossRunningInstances(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolPrioritySettingsRepo{}
	control := NewSettingService(repo, nil)
	gateway := NewSettingService(repo, nil)
	for _, mode := range []FastPoolPriorityMode{FastPoolPriorityRate, FastPoolPriorityIntelligence} {
		saved, err := control.UpdateFastPoolPrioritySettings(ctx, mode)
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
		_, err := control.UpdateFastPoolPrioritySettings(ctx, mode)
		require.Error(t, err)
	}
	require.Equal(t, 2, repo.writes)
	repo.setErr = errors.New("write failed")
	_, err := control.UpdateFastPoolPrioritySettings(ctx, FastPoolPriorityRate)
	require.Error(t, err)
	require.Equal(t, string(FastPoolPriorityIntelligence), repo.values[SettingKeyFastPoolPriorityMode])
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
		mode FastPoolPriorityMode
		want []int64
	}{
		{FastPoolPriorityIntelligence, []int64{3, 2, 1, 4}},
		{FastPoolPriorityRate, []int64{1, 2, 3, 4}},
		{FastPoolPriorityIntelligence, []int64{3, 2, 1, 4}},
	} {
		_, err := control.UpdateFastPoolPrioritySettings(ctx, scenario.mode)
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
