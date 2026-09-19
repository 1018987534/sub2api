package service

import (
	"context"
	"log/slog"
	"math"
	"time"
)

const (
	groupCacheRateStatsTTL        = 30 * time.Second
	groupCacheRateStatsFailureTTL = 5 * time.Second
	groupCacheRateEpsilon         = 1e-9
	groupCacheRateFilterReason    = "cache_rate_below_minimum"
)

type cachedAccountCacheStats struct {
	stats     AccountCacheStats
	available bool
	expiresAt time.Time
}

// loadAccountCacheStatsCached returns one snapshot for every requested ID.
// available=false means the provider could not be read and callers must
// fail-open. A successful query with no row is available=true with a zero
// denominator. That means the account has no calculable cache rate yet, so it
// stays eligible until real usage exists and the threshold can be evaluated.
func (s *RateLimitService) loadAccountCacheStatsCached(ctx context.Context, accountIDs []int64) map[int64]cachedAccountCacheStats {
	result := make(map[int64]cachedAccountCacheStats, len(accountIDs))
	if s == nil || len(accountIDs) == 0 {
		return result
	}

	now := time.Now()
	missing := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, duplicate := seen[accountID]; duplicate {
			continue
		}
		seen[accountID] = struct{}{}
		entry, ok := s.accountCacheStats.Load(accountID)
		cached, _ := entry.(cachedAccountCacheStats)
		if ok && now.Before(cached.expiresAt) {
			result[accountID] = cached
			continue
		}
		missing = append(missing, accountID)
	}

	if len(missing) == 0 {
		return result
	}
	provider, ok := s.usageRepo.(AccountCacheStatsProvider)
	if !ok || provider == nil {
		return result
	}

	stats, err := provider.GetAccountCacheStatsBatch(ctx, missing, now.Add(-24*time.Hour), now)
	available := err == nil
	ttl := groupCacheRateStatsTTL
	if err != nil {
		ttl = groupCacheRateStatsFailureTTL
		slog.Warn("group_cache_rate_stats_load_failed", "account_count", len(missing), "error", err)
	}

	for _, accountID := range missing {
		entry := cachedAccountCacheStats{available: available, expiresAt: now.Add(ttl)}
		if available {
			entry.stats = stats[accountID]
		}
		s.accountCacheStats.Store(accountID, entry)
		result[accountID] = entry
	}
	return result
}

func validEnabledGroupMinCacheRate(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= 1
}

func groupCacheRateBelowMinimum(stats AccountCacheStats, minimum float64) bool {
	if stats.CacheRateDenominator <= 0 {
		return false
	}
	rate := float64(stats.CacheReadTokens) / float64(stats.CacheRateDenominator)
	return rate+groupCacheRateEpsilon < minimum
}

func (s *defaultOpenAIAccountScheduler) warmGroupCacheRateStats(ctx context.Context, accounts []Account, req OpenAIAccountScheduleRequest) {
	if s == nil || s.service == nil || s.service.rateLimitService == nil ||
		!req.FirstTokenPriority || !validEnabledGroupMinCacheRate(req.MinCacheRate) {
		return
	}
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey {
			ids = append(ids, account.ID)
		}
	}
	_ = s.service.rateLimitService.loadAccountCacheStatsCached(ctx, ids)
}

func (s *defaultOpenAIAccountScheduler) isAccountGroupCacheRateCompatible(ctx context.Context, account *Account, req OpenAIAccountScheduleRequest) (bool, string) {
	if account == nil || !req.FirstTokenPriority || !validEnabledGroupMinCacheRate(req.MinCacheRate) {
		return true, ""
	}
	// The existing cache-rate aggregate is defined for OpenAI API-key traffic.
	// OAuth and other OpenAI-compatible platforms stay eligible rather than
	// being rejected for a metric they do not produce.
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return true, ""
	}
	if s == nil || s.service == nil || s.service.rateLimitService == nil {
		return true, ""
	}
	entry, ok := s.service.rateLimitService.loadAccountCacheStatsCached(ctx, []int64{account.ID})[account.ID]
	if !ok || !entry.available {
		return true, "" // fail-open on provider/configuration failures
	}
	if groupCacheRateBelowMinimum(entry.stats, req.MinCacheRate) {
		return false, groupCacheRateFilterReason
	}
	return true, ""
}

func (s *OpenAIGatewayService) loadOpenAIGroupMinCacheRate(ctx context.Context, groupID *int64) float64 {
	if s == nil || groupID == nil || *groupID <= 0 || s.schedulerSnapshot == nil {
		return 0
	}
	group, err := s.schedulerSnapshot.GetGroupByIDLite(ctx, *groupID)
	if err != nil {
		slog.Warn("group_cache_rate_group_load_failed", "group_id", *groupID, "error", err)
		return 0
	}
	if group == nil || group.Platform != PlatformOpenAI || !validEnabledGroupMinCacheRate(group.MinCacheRate) {
		return 0
	}
	return group.MinCacheRate
}
