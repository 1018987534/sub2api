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

type apiKeyGroupRouteGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
}

func (s *apiKeyGroupRouteGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	return s.group(id)
}

func (s *apiKeyGroupRouteGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	return s.group(id)
}

func (s *apiKeyGroupRouteGroupRepoStub) group(id int64) (*Group, error) {
	group := s.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	clone := *group
	return &clone, nil
}

type apiKeyGroupRouteAccountRepoStub struct {
	AccountRepository
	accounts   []Account
	groupCalls []int64
}

type apiKeyGroupRouteSubscriptionRepoStub struct {
	UserSubscriptionRepository
	subscription *UserSubscription
	err          error
}

func (s *apiKeyGroupRouteSubscriptionRepoStub) GetActiveByUserIDAndGroupID(_ context.Context, _, _ int64) (*UserSubscription, error) {
	return s.subscription, s.err
}

func (s *apiKeyGroupRouteAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range s.accounts {
		if s.accounts[i].ID == id {
			clone := s.accounts[i]
			return &clone, nil
		}
	}
	return nil, errors.New("account not found")
}

func (s *apiKeyGroupRouteAccountRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	s.groupCalls = append(s.groupCalls, groupID)
	var result []Account
	for _, account := range s.accounts {
		if account.Platform == platform && openAIStickyAccountMatchesGroup(&account, &groupID) {
			result = append(result, account)
		}
	}
	return result, nil
}

func TestAPIKeyRouteRateCapUsesUserOverrideAndPeakMultiplier(t *testing.T) {
	cap := 1.5
	route := APIKeyGroupRoute{GroupID: 7, MaxRateMultiplier: &cap}
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 2}
	ctx := ContextWithAPIKeyGroupRoutes(context.Background(), &APIKey{
		GroupRoutes: []APIKeyGroupRoute{route},
		User:        &User{ID: 42},
	})

	allowed := apiKeyRouteWithinRateCap(ctx, route, group, func(context.Context, int64, int64, float64) float64 {
		return 1.25
	}, true)
	require.True(t, allowed)

	noOverride := context.Background()
	require.False(t, apiKeyRouteWithinRateCap(noOverride, route, group, nil, true))
}

func TestAPIKeyRouteRateCapNilMeansUnlimited(t *testing.T) {
	route := APIKeyGroupRoute{GroupID: 7}
	group := &Group{ID: 7, Platform: PlatformAnthropic, Status: StatusActive, RateMultiplier: 100}
	require.True(t, apiKeyRouteWithinRateCap(context.Background(), route, group, nil, false))
}

func TestAPIKeyGroupRouteContextPreservesLegacyEffectiveRoute(t *testing.T) {
	groupID := int64(9)
	legacy := &APIKey{GroupID: &groupID}
	require.Equal(t, []APIKeyGroupRoute{{GroupID: groupID}}, legacy.EffectiveGroupRoutes())

	configured := &APIKey{GroupID: &groupID, GroupRoutes: []APIKeyGroupRoute{{GroupID: groupID}, {GroupID: 10}}}
	ctx := ContextWithAPIKeyGroupRoutes(context.Background(), configured)
	routes, ok := apiKeyGroupRoutesFromContext(ctx, &groupID)
	require.True(t, ok)
	require.Len(t, routes.routes, 2)
}

func TestAPIKeyServiceValidateGroupRoutesRejectsInvalidConfigurations(t *testing.T) {
	groups := map[int64]*Group{
		1: {ID: 1, Platform: PlatformOpenAI, Status: StatusActive},
		2: {ID: 2, Platform: PlatformAnthropic, Status: StatusActive},
		3: {ID: 3, Platform: PlatformOpenAI, Status: StatusDisabled},
		4: {ID: 4, Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true},
	}
	svc := &APIKeyService{groupRepo: &apiKeyGroupRouteGroupRepoStub{groups: groups}}
	user := &User{ID: 42}
	validCap := 1.5
	invalidCap := math.Inf(1)

	tests := []struct {
		name   string
		routes []APIKeyGroupRoute
	}{
		{name: "duplicate group", routes: []APIKeyGroupRoute{{GroupID: 1}, {GroupID: 1}}},
		{name: "mixed platform", routes: []APIKeyGroupRoute{{GroupID: 1}, {GroupID: 2}}},
		{name: "inactive group", routes: []APIKeyGroupRoute{{GroupID: 3}}},
		{name: "unauthorized exclusive group", routes: []APIKeyGroupRoute{{GroupID: 4}}},
		{name: "invalid rate cap", routes: []APIKeyGroupRoute{{GroupID: 1, MaxRateMultiplier: &invalidCap}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := svc.validateAPIKeyGroupRoutes(context.Background(), user, tt.routes)
			require.Error(t, err)
		})
	}

	routes, primary, err := svc.validateAPIKeyGroupRoutes(context.Background(), user, []APIKeyGroupRoute{{GroupID: 1, MaxRateMultiplier: &validCap}})
	require.NoError(t, err)
	require.Equal(t, int64(1), primary.ID)
	require.Equal(t, []APIKeyGroupRoute{{GroupID: 1, MaxRateMultiplier: &validCap}}, routes)
}

func TestAPIKeyAuthSnapshotRoundTripPreservesOrderedGroupRoutes(t *testing.T) {
	cap := 0.55
	groupID := int64(11)
	apiKey := &APIKey{
		ID:          1,
		UserID:      2,
		GroupID:     &groupID,
		GroupRoutes: []APIKeyGroupRoute{{GroupID: 11, MaxRateMultiplier: &cap}, {GroupID: 12}},
		Status:      StatusActive,
		User:        &User{ID: 2, Status: StatusActive},
		Group:       &Group{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
	}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, &config.Config{})

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	require.Equal(t, apiKeyAuthSnapshotVersion, snapshot.Version)
	restored, used, err := svc.applyAuthCacheEntry("sk-route-roundtrip", &APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, apiKey.GroupRoutes, restored.GroupRoutes)
}

func TestAPIKeyRouteBillingEligibilitySkipsUnavailableBillingModes(t *testing.T) {
	now := time.Now()
	dailyLimit := 1.0
	user := &User{ID: 42, Balance: 0}
	standard := &Group{ID: 1, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard}
	_, eligible, err := resolveAPIKeyRouteBillingEligibility(context.Background(), nil, user, standard)
	require.NoError(t, err)
	require.False(t, eligible)

	subscriptionGroup := &Group{
		ID: 2, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription,
		IsExclusive: true, DailyLimitUSD: &dailyLimit,
	}
	require.True(t, apiKeyRouteAllowedForUser(user, subscriptionGroup), "an active subscription, not allowed_groups, authorizes subscription routes")

	validSub := &UserSubscription{
		ID: 10, UserID: user.ID, GroupID: subscriptionGroup.ID,
		Status: SubscriptionStatusActive, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now,
	}
	repo := &apiKeyGroupRouteSubscriptionRepoStub{subscription: validSub}
	resolved, eligible, err := resolveAPIKeyRouteBillingEligibility(context.Background(), repo, user, subscriptionGroup)
	require.NoError(t, err)
	require.True(t, eligible)
	require.Equal(t, validSub.ID, resolved.ID)

	overLimit := *validSub
	overLimit.DailyUsageUSD = 1.01
	repo.subscription = &overLimit
	_, eligible, err = resolveAPIKeyRouteBillingEligibility(context.Background(), repo, user, subscriptionGroup)
	require.NoError(t, err)
	require.False(t, eligible)
}

func TestOpenAIGatewayOrderedRoutesFallBackToNextGroup(t *testing.T) {
	groups := &apiKeyGroupRouteGroupRepoStub{groups: map[int64]*Group{
		1: {ID: 1, Name: "primary", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1},
		2: {ID: 2, Name: "backup", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1},
	}}
	accounts := &apiKeyGroupRouteAccountRepoStub{accounts: []Account{{
		ID: 200, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Schedulable: true, Concurrency: 1, GroupIDs: []int64{2},
	}}}
	snapshot := NewSchedulerSnapshotService(nil, nil, accounts, groups, nil)
	svc := &OpenAIGatewayService{
		accountRepo:       accounts,
		cfg:               &config.Config{},
		schedulerSnapshot: snapshot,
	}
	primaryID := int64(1)
	ctx := ContextWithAPIKeyGroupRoutes(context.Background(), &APIKey{
		GroupID:     &primaryID,
		GroupRoutes: []APIKeyGroupRoute{{GroupID: 1}, {GroupID: 2}},
		User:        &User{ID: 42, Status: StatusActive, Balance: 10},
	})

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &primaryID, "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, accounts.groupCalls)
	require.Equal(t, int64(200), selection.Account.ID)
	require.Equal(t, int64(2), selection.Group.ID)
	require.Equal(t, 1, selection.GroupRouteIndex)
	require.Equal(t, int64(2), selection.Account.SelectedAPIKeyGroup.ID)
}
