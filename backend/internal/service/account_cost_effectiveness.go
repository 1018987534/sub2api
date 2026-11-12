package service

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// This is a normalized input-cost estimate, not a model-specific invoice.
// The same 10% cache-read weight is used by the dashboard and group ordering.
const costEffectivenessCacheReadWeight = 0.1

func effectiveInputRateMultiplier(rate *float64, stats AccountCacheStats) *float64 {
	if rate == nil || *rate < 0 || math.IsNaN(*rate) || math.IsInf(*rate, 0) ||
		stats.CacheRateDenominator <= 0 || stats.CacheReadTokens < 0 || stats.CacheReadTokens > stats.CacheRateDenominator {
		return nil
	}
	cacheRate := float64(stats.CacheReadTokens) / float64(stats.CacheRateDenominator)
	value := *rate * (1 - cacheRate + cacheRate*costEffectivenessCacheReadWeight)
	return &value
}

func firstTokenSchedulingGroupID(ctx context.Context, req *OpenAIAccountScheduleRequest) int64 {
	if req != nil {
		if req.GroupID != nil {
			return *req.GroupID
		}
		return 0
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil {
		return group.ID
	}
	return 0
}

// Partitioning happens before this sort, so even an extremely cheap slow
// account cannot outrank a confirmed fast account. Unknown costs stay last.
func orderCostEffectivePool(ctx context.Context, accountIDs []int64, candidates []*Account, latency map[int64]FirstTokenLatencyStats, svc *RateLimitService, now time.Time) {
	cacheStats := svc.loadAccountCacheStatsCached(ctx, accountIDs)
	costs := make(map[int64]*float64, len(accountIDs))
	for _, account := range candidates {
		if account == nil {
			continue
		}
		entry := cacheStats[account.ID]
		if rate, ok := openAIFreshUpstreamBillingRate(account, now); ok && entry.available {
			costs[account.ID] = effectiveInputRateMultiplier(&rate, entry.stats)
		}
	}
	sort.SliceStable(accountIDs, func(i, j int) bool {
		left, right := costs[accountIDs[i]], costs[accountIDs[j]]
		if (left == nil) != (right == nil) {
			return left != nil
		}
		if left != nil && *left != *right {
			return *left < *right
		}
		if left == nil {
			return false // Preserve the existing fallback for unmeasured accounts.
		}
		if latency[accountIDs[i]].PredictedMS != latency[accountIDs[j]].PredictedMS {
			return latency[accountIDs[i]].PredictedMS < latency[accountIDs[j]].PredictedMS
		}
		return accountIDs[i] < accountIDs[j]
	})
}
