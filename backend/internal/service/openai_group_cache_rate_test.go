package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cacheRateProviderStub struct {
	UsageLogRepository
	stats map[int64]AccountCacheStats
	err   error
}

func (s cacheRateProviderStub) GetAccountCacheStatsBatch(_ context.Context, _ []int64, _, _ time.Time) (map[int64]AccountCacheStats, error) {
	return s.stats, s.err
}

func cacheRateTestAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
	}
}

func TestGroupCacheRateGateFiltersBelowMinimum(t *testing.T) {
	provider := cacheRateProviderStub{stats: map[int64]AccountCacheStats{
		1: {CacheReadTokens: 72, CacheRateDenominator: 100},
		2: {CacheReadTokens: 80, CacheRateDenominator: 100},
	}}
	service := &OpenAIGatewayService{rateLimitService: &RateLimitService{usageRepo: provider}}
	scheduler := &defaultOpenAIAccountScheduler{service: service}
	req := OpenAIAccountScheduleRequest{FirstTokenPriority: true, MinCacheRate: 0.8}

	ok, reason := scheduler.isAccountGroupCacheRateCompatible(context.Background(), cacheRateTestAccount(1), req)
	require.False(t, ok)
	require.Equal(t, groupCacheRateFilterReason, reason)
	ok, reason = scheduler.isAccountGroupCacheRateCompatible(context.Background(), cacheRateTestAccount(2), req)
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestGroupCacheRateGateTreatsMissingSamplesAsBelowMinimum(t *testing.T) {
	service := &OpenAIGatewayService{rateLimitService: &RateLimitService{
		usageRepo: cacheRateProviderStub{stats: map[int64]AccountCacheStats{}},
	}}
	scheduler := &defaultOpenAIAccountScheduler{service: service}
	ok, reason := scheduler.isAccountGroupCacheRateCompatible(
		context.Background(), cacheRateTestAccount(3),
		OpenAIAccountScheduleRequest{FirstTokenPriority: true, MinCacheRate: 0.8},
	)
	require.False(t, ok)
	require.Equal(t, groupCacheRateFilterReason, reason)
}

func TestGroupCacheRateGateFailsOpenOnStatsProviderError(t *testing.T) {
	service := &OpenAIGatewayService{rateLimitService: &RateLimitService{usageRepo: cacheRateProviderStub{err: errors.New("db unavailable")}}}
	scheduler := &defaultOpenAIAccountScheduler{service: service}
	ok, reason := scheduler.isAccountGroupCacheRateCompatible(
		context.Background(), cacheRateTestAccount(4),
		OpenAIAccountScheduleRequest{FirstTokenPriority: true, MinCacheRate: 0.8},
	)
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestGroupCacheRateGateDisabledAndNonAPIKeyAreUnchanged(t *testing.T) {
	service := &OpenAIGatewayService{rateLimitService: &RateLimitService{usageRepo: cacheRateProviderStub{stats: map[int64]AccountCacheStats{}}}}
	scheduler := &defaultOpenAIAccountScheduler{service: service}
	ctx := context.Background()
	ok, reason := scheduler.isAccountGroupCacheRateCompatible(ctx, cacheRateTestAccount(5), OpenAIAccountScheduleRequest{FirstTokenPriority: true})
	require.True(t, ok)
	require.Empty(t, reason)
	oauth := cacheRateTestAccount(6)
	oauth.Type = AccountTypeOAuth
	ok, reason = scheduler.isAccountGroupCacheRateCompatible(ctx, oauth, OpenAIAccountScheduleRequest{FirstTokenPriority: true, MinCacheRate: 0.8})
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestGroupCacheRateGateCannotBeBypassedByStickySelection(t *testing.T) {
	groupID := int64(91)
	low := cacheRateTestAccount(11)
	high := cacheRateTestAccount(12)
	low.GroupIDs = []int64{groupID}
	high.GroupIDs = []int64{groupID}
	low.Concurrency = 1
	high.Concurrency = 1

	gateway := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*low, *high}},
		cache:       &schedulerTestGatewayCache{},
		rateLimitService: &RateLimitService{usageRepo: cacheRateProviderStub{stats: map[int64]AccountCacheStats{
			low.ID:  {CacheReadTokens: 72, CacheRateDenominator: 100},
			high.ID: {CacheReadTokens: 80, CacheRateDenominator: 100},
		}}},
	}
	scheduler := &defaultOpenAIAccountScheduler{service: gateway, stats: newOpenAIAccountRuntimeStats()}
	selection, decision, err := scheduler.Select(context.Background(), OpenAIAccountScheduleRequest{
		GroupID:            &groupID,
		Platform:           PlatformOpenAI,
		SessionHash:        "sticky-cache-rate",
		StickyAccountID:    low.ID,
		FirstTokenPriority: true,
		MinCacheRate:       0.8,
	})

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, high.ID, selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestValidateGroupMinCacheRate(t *testing.T) {
	for _, value := range []float64{0, 0.8, 1} {
		require.NoError(t, ValidateGroupMinCacheRate(value))
	}
	for _, value := range []float64{-0.01, 1.01} {
		require.Error(t, ValidateGroupMinCacheRate(value))
	}
}
