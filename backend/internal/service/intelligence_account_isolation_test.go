package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type sharedIntelligenceRepo struct {
	intelligenceProtectionRepo
	byGroup map[int64][]int64
}

func (r *sharedIntelligenceRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, _ string) ([]Account, error) {
	var result []Account
	for _, id := range r.byGroup[groupID] {
		for _, a := range r.accounts {
			if a.ID == id {
				result = append(result, a)
			}
		}
	}
	return result, nil
}
func TestIntelligenceSharedAccountIsolationAndRecovery(t *testing.T) {
	openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil))
	t.Cleanup(func() { openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil)) })
	g1 := Group{ID: 111, Name: "010不降智", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1}
	g2 := g1
	g2.ID = 5
	g2.Name = "013不降智"
	g3 := g1
	g3.ID = 79
	g3.Name = "020不降智"
	a, b := cacheRateTestAccount(1), cacheRateTestAccount(2)
	recentUsage := time.Now().Add(-time.Hour)
	b.LastUsedAt = &recentUsage
	a.GroupIDs = []int64{111, 5, 79}
	b.GroupIDs = []int64{5, 79}
	a.Groups = []*Group{&g1, &g2, &g3}
	repo := &sharedIntelligenceRepo{intelligenceProtectionRepo: intelligenceProtectionRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*a, *b}}, groups: []Group{g1, g2, g3}}, byGroup: map[int64][]int64{111: {1}, 5: {1, 2}, 79: {1, 2}}}
	cache := &intelligenceResetCache{}
	settings := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyTotalDurationPriorityEnabled: "true"}}
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{firstTokenLatencyStatsCache: cache, usageRepo: cacheRateProviderStub{stats: map[int64]AccountCacheStats{}}, settingService: &SettingService{settingRepo: settings}}}
	ctx := context.Background()
	legacyUntil := time.Now().Add(20 * time.Minute)
	repo.accounts[0].TempUnschedulableUntil = &legacyUntil
	repo.accounts[0].TempUnschedulableReason = "intelligence: old global pause"
	paused, err := svc.ProtectDegradedIntelligenceAccount(ctx, &g1, 1, "gpt-6.1-sol", 0)
	require.NoError(t, err)
	require.True(t, paused)
	require.Equal(t, int64(1), cache.resetID)
	account, err := repo.GetByID(ctx, 1)
	require.NoError(t, err)
	require.Nil(t, account.TempUnschedulableUntil)
	scheduler := &defaultOpenAIAccountScheduler{service: svc}
	for _, group := range []Group{g1, g2, g3} {
		req := OpenAIAccountScheduleRequest{GroupID: &group.ID, Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol"}
		ok, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, req)
		require.Equal(t, group.ID == 111, ok)
		if !ok {
			require.Equal(t, "intelligence_isolated", reason)
		}
		require.Equal(t, group.ID == 111, svc.openAIAccountMatchesSchedulingGroup(ctx, account, &group.ID))
		require.Equal(t, group.ID == 111, svc.recheckSelectedOpenAIAccountFromDB(ctx, account, &group.ID, PlatformOpenAI, "gpt-6.1-sol", false, OpenAIEndpointCapability("")) != nil)
		t.Logf("account=1 group=%d schedulable=%v", group.ID, ok)
	}
	for _, test := range []struct {
		group int64
		want  int64
	}{{111, 1}, {5, 2}, {79, 2}} {
		selection, _, err := scheduler.Select(ctx, OpenAIAccountScheduleRequest{GroupID: &test.group, Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", FirstTokenPriority: true})
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.Equal(t, test.want, selection.Account.ID)
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		t.Logf("actual Select: group=%d account=%d", test.group, selection.Account.ID)
	}
	raw, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	copy := *account
	copy.Extra = decoded
	require.False(t, intelligenceAccountBlocked(&copy, &g1.ID, time.Now()))
	require.True(t, intelligenceAccountBlocked(&copy, &g2.ID, time.Now().Add(time.Hour)))
	cache.stats = map[int64]FirstTokenLatencyStats{1: {PredictedMS: 1000, SampleCount: 30, ReliableFast: true, UpdatedAt: time.Now()}}
	metrics, err := svc.rateLimitService.AccountFirstTokenLatencyMetrics(ctx, []Account{*account})
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.True(t, metrics[0].HasPrediction)
	require.Equal(t, float64(1000), metrics[0].NormalTotalMS)
	require.Equal(t, int64(30), metrics[0].SampleCount)
	require.True(t, metrics[0].IsFastPool)
	for _, g := range metrics[0].Groups {
		require.Equal(t, g.GroupID != 111, g.IntelligenceBlocked)
		require.Equal(t, g.GroupID == 111, g.IntelligenceExempt)
	}
	until := time.Now().Add(time.Hour)
	account.TempUnschedulableUntil = &until
	account.TempUnschedulableReason = "unrelated"
	require.NoError(t, svc.RecoverIntelligenceAccount(ctx, 1))
	require.True(t, intelligenceAccountBlocked(account, &g2.ID, time.Now()))
	account.Extra[intelligencePauseUntilKey] = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	require.NoError(t, svc.RecoverIntelligenceAccount(ctx, 1))
	for _, group := range []Group{g1, g2, g3} {
		require.False(t, intelligenceAccountBlocked(account, &group.ID, time.Now()))
	}
	require.Equal(t, "unrelated", account.TempUnschedulableReason)
	require.Equal(t, []int64{111, 5, 79}, account.GroupIDs)
	t.Log("normal result after minimum pause: all group restrictions removed; membership and unrelated cooldown preserved")
}

func TestIntelligenceIsolationExpiresWithoutSoleGroupException(t *testing.T) {
	account := cacheRateTestAccount(1)
	account.Extra = map[string]any{intelligencePauseUntilKey: time.Now().Add(20 * time.Minute).Format(time.RFC3339Nano), intelligenceAllowedGroupsKey: []int64{}, intelligenceRecoveryRequiredKey: false}
	groupID := int64(5)
	require.True(t, intelligenceAccountBlocked(account, &groupID, time.Now()))
	require.False(t, intelligenceAccountBlocked(account, &groupID, time.Now().Add(21*time.Minute)))
}

func TestIntelligenceFallbackAfterNormalAccountFails(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	group := &Group{ID: 98, Name: "不降智-特价", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	model := "gpt-6.2-sol"
	paused, normal := cacheRateTestAccount(1), cacheRateTestAccount(2)
	for _, account := range []*Account{paused, normal} {
		account.GroupIDs = []int64{group.ID}
		account.Groups = []*Group{group}
	}
	generation := time.Now().Add(20 * time.Minute).Format(time.RFC3339Nano)
	paused.Extra = map[string]any{
		intelligencePauseUntilKey:       generation,
		intelligenceRecoveryRequiredKey: true,
		intelligenceProtectedGroupsKey:  []int64{group.ID},
	}
	repo := &schedulerTestOpenAIAccountRepo{accounts: []Account{*paused, *normal}}
	cache := &openAISnapshotCacheStub{snapshotAccounts: []*Account{paused, normal}, accountsByID: map[int64]*Account{paused.ID: paused, normal.ID: normal}}
	settings := &fastPoolPrioritySettingsRepo{openAIAdvancedSchedulerSettingRepoStub: openAIAdvancedSchedulerSettingRepoStub{
		values: map[string]string{SettingKeyTotalDurationPriorityEnabled: "true"},
	}}
	gateway := &OpenAIGatewayService{
		cfg: &config.Config{}, accountRepo: repo,
		schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, repo, nil, nil),
		rateLimitService:  &RateLimitService{settingService: NewSettingService(settings, nil)},
	}

	selectAccount := func(previous string, excluded map[int64]struct{}) (*AccountSelectionResult, error) {
		result, _, err := gateway.SelectAccountWithSchedulerForCapability(ctx, &group.ID, previous, "", model, excluded, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityResponses, false, false, false)
		return result, err
	}
	requireSelection := func(want int64, previous string, excluded map[int64]struct{}) {
		t.Helper()
		result, err := selectAccount(previous, excluded)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, want, result.Account.ID)
		result.ReleaseFunc()
	}

	requireSelection(normal.ID, "", nil)
	excluded := map[int64]struct{}{normal.ID: {}}
	requireSelection(paused.ID, "", excluded)
	require.Equal(t, map[int64]struct{}{normal.ID: {}}, excluded)
	require.True(t, gateway.intelligenceAccountBlockedForGroup(ctx, paused, &group.ID, time.Now()))
	require.Equal(t, generation, paused.GetExtraString(intelligencePauseUntilKey))
	requireSelection(normal.ID, "", nil)

	_, err := selectAccount("resp_pinned", excluded)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	_, err = selectAccount("", map[int64]struct{}{normal.ID: {}, paused.ID: {}})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)

	// The DB recheck must still observe an independent cooldown after a cached
	// snapshot considered the paused account schedulable.
	until := time.Now().Add(time.Hour)
	repo.accounts[0].TempUnschedulableUntil = &until
	_, err = selectAccount("", excluded)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	repo.accounts[0].TempUnschedulableUntil = nil

	_, err = gateway.rateLimitService.settingService.UpdateFastPoolCostEffectivenessGroup(ctx, group.ID, true)
	require.NoError(t, err)
	_, err = selectAccount("", excluded)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestIntelligenceProtectionIgnoresOrdinaryGroups(t *testing.T) {
	openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil))
	t.Cleanup(func() { openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil)) })
	protected := Group{ID: 111, Name: "010不降智", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1}
	ordinary := Group{ID: 999, Name: "测试", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1}
	a, b := cacheRateTestAccount(1), cacheRateTestAccount(2)
	recentUsage := time.Now().Add(-time.Hour)
	b.LastUsedAt = &recentUsage
	a.GroupIDs = []int64{protected.ID, ordinary.ID}
	a.Groups = []*Group{&protected, &ordinary}
	b.GroupIDs = []int64{protected.ID, ordinary.ID}
	repo := &sharedIntelligenceRepo{intelligenceProtectionRepo: intelligenceProtectionRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*a, *b}}, groups: []Group{protected, ordinary}}, byGroup: map[int64][]int64{protected.ID: {1, 2}, ordinary.ID: {1}}}
	cache := &intelligenceResetCache{}
	settings := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyTotalDurationPriorityEnabled: "true"}}
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{firstTokenLatencyStatsCache: cache, usageRepo: cacheRateProviderStub{stats: map[int64]AccountCacheStats{}}, settingService: &SettingService{settingRepo: settings}}}
	paused, err := svc.ProtectDegradedIntelligenceAccount(context.Background(), &protected, 1, "gpt-6.1-sol", 0)
	require.NoError(t, err)
	require.True(t, paused)
	account, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, intelligenceAllowedGroups(account))
	require.Equal(t, true, account.Extra[intelligenceRecoveryRequiredKey])
	require.Equal(t, []int64{protected.ID}, intelligenceGroupIDList(account.Extra[intelligenceProtectedGroupsKey]))
	require.False(t, svc.intelligenceAccountBlockedForGroup(context.Background(), account, &ordinary.ID, time.Now()))
	require.True(t, svc.intelligenceAccountBlockedForGroup(context.Background(), account, &protected.ID, time.Now()))
	expired := time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	account.Extra[intelligencePauseUntilKey] = expired
	account.Extra[intelligenceAllowedGroupsKey] = []int64{ordinary.ID}
	account.Extra[intelligenceRecoveryRequiredKey] = true
	paused, err = svc.ProtectDegradedIntelligenceAccount(context.Background(), &protected, account.ID, "gpt-6.1-sol", 0)
	require.NoError(t, err)
	require.True(t, paused)
	require.Equal(t, expired, account.GetExtraString(intelligencePauseUntilKey))
	require.Equal(t, true, account.Extra[intelligenceRecoveryRequiredKey])
	require.True(t, svc.intelligenceAccountBlockedForGroup(context.Background(), account, &protected.ID, time.Now()))
}
