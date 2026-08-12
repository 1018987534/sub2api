package service

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

const (
	firstTokenPriorityMinimumSamples = 3
	firstTokenPriorityFreshFor       = 20 * time.Minute
	firstTokenPriorityNearTieRatio   = 0.05
	firstTokenPriorityFastThreshold  = 10_000.0
	firstTokenPriorityProbeBase      = 2 * time.Minute
	firstTokenPriorityProbeMax       = 6 * time.Hour
	firstTokenPriorityProbeLease     = 10 * time.Minute
)

type firstTokenRankedAccount struct {
	id       int64
	stats    FirstTokenLatencyStats
	known    bool
	original int
}

type AccountFirstTokenLatencyMetric struct {
	AccountID            int64     `json:"account_id"`
	AccountName          string    `json:"account_name"`
	PredictedMS          float64   `json:"predicted_ms"`
	SampleCount          int64     `json:"sample_count"`
	UpdatedAt            time.Time `json:"updated_at"`
	SlowStreak           int       `json:"slow_streak"`
	ProbeIntervalSeconds int64     `json:"probe_interval_seconds"`
}

func isFirstTokenPriorityAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey && account.IsActive() && account.Schedulable
}

func (s *RateLimitService) AccountFirstTokenLatencyMetrics(ctx context.Context, accounts []Account) ([]AccountFirstTokenLatencyMetric, error) {
	if s == nil || s.firstTokenLatencyStatsCache == nil || len(accounts) == 0 {
		return []AccountFirstTokenLatencyMetric{}, nil
	}
	eligible := make([]Account, 0, len(accounts))
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if !isFirstTokenPriorityAccount(&account) {
			continue
		}
		eligible = append(eligible, account)
		accountIDs = append(accountIDs, account.ID)
	}
	stats, err := s.firstTokenLatencyStatsCache.GetStatsBatch(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	fastestMS := 0.0
	for _, stat := range stats {
		if stat.PredictedMS > 0 && (fastestMS == 0 || stat.PredictedMS < fastestMS) {
			fastestMS = stat.PredictedMS
		}
	}
	metrics := make([]AccountFirstTokenLatencyMetric, 0, len(stats))
	for _, account := range eligible {
		stat, ok := stats[account.ID]
		if !ok || stat.PredictedMS <= 0 {
			continue
		}
		metrics = append(metrics, AccountFirstTokenLatencyMetric{
			AccountID:            account.ID,
			AccountName:          account.Name,
			PredictedMS:          stat.PredictedMS,
			SampleCount:          stat.SampleCount,
			UpdatedAt:            stat.UpdatedAt,
			SlowStreak:           stat.SlowStreak,
			ProbeIntervalSeconds: int64(firstTokenPriorityProbeInterval(stat, fastestMS).Seconds()),
		})
	}
	sort.SliceStable(metrics, func(i, j int) bool {
		if metrics[i].PredictedMS == metrics[j].PredictedMS {
			return metrics[i].AccountID < metrics[j].AccountID
		}
		return metrics[i].PredictedMS < metrics[j].PredictedMS
	})
	return metrics, nil
}

func (s *RateLimitService) ObserveFirstTokenLatency(ctx context.Context, account *Account, requestID string, firstTokenMs *int) {
	if s == nil || s.firstTokenLatencyStatsCache == nil || !isFirstTokenPriorityAccount(account) || firstTokenMs == nil || account.ID <= 0 || *firstTokenMs <= 0 {
		return
	}
	if err := s.firstTokenLatencyStatsCache.RecordSample(ctx, account.ID, requestID, *firstTokenMs); err != nil {
		slog.Warn("first_token_latency_stats_record_failed", "account_id", account.ID, "error", err)
	}
}

func (s *RateLimitService) FirstTokenPriorityEnabled(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.FirstTokenPriorityEnabled(ctx)
}

func (s *SettingService) FirstTokenPriorityEnabled(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	gateway := &OpenAIGatewayService{rateLimitService: &RateLimitService{settingService: s}}
	return gateway.openAIAdvancedSchedulerRuntimeSettings(ctx).firstTokenPriorityEnabled
}

// firstTokenPriorityOrder returns every candidate. Known/fresh accounts are
// ordered by predicted TTFT for exploitation. When an adaptive probe is due,
// a shared lease promotes exactly one account across all gateway instances.
func firstTokenPriorityOrder(ctx context.Context, candidates []*Account, cache FirstTokenLatencyStatsCache) []int64 {
	accountIDs := make([]int64, 0, len(candidates))
	priorityAccountIDs := make([]int64, 0, len(candidates))
	fallbackAccountIDs := make([]int64, 0, len(candidates))
	for _, account := range candidates {
		if account != nil {
			accountIDs = append(accountIDs, account.ID)
			if isFirstTokenPriorityAccount(account) {
				priorityAccountIDs = append(priorityAccountIDs, account.ID)
			} else {
				fallbackAccountIDs = append(fallbackAccountIDs, account.ID)
			}
		}
	}
	if len(accountIDs) <= 1 || len(priorityAccountIDs) == 0 || cache == nil {
		return accountIDs
	}
	stats, err := cache.GetStatsBatch(ctx, priorityAccountIDs)
	if err != nil {
		return accountIDs
	}
	now := time.Now()
	allPriorityAccountsFast := true
	for _, accountID := range priorityAccountIDs {
		stat, found := stats[accountID]
		age := now.Sub(stat.UpdatedAt)
		if !found || !firstTokenPriorityStatsReliable(stat) || age < 0 || age > firstTokenPriorityFreshFor || stat.PredictedMS <= 0 || stat.PredictedMS > firstTokenPriorityFastThreshold {
			allPriorityAccountsFast = false
			break
		}
	}
	if allPriorityAccountsFast {
		withProbe := firstTokenPriorityOrderWithStats(priorityAccountIDs, stats, now, true)
		if len(withProbe) == 0 || withProbe[0] == priorityAccountIDs[0] {
			return accountIDs
		}
		claimed, claimErr := cache.TryClaimProbe(ctx, withProbe[0], firstTokenPriorityProbeLease)
		if claimErr != nil || !claimed {
			return accountIDs
		}
		return promoteFirstTokenAccount(accountIDs, withProbe[0])
	}
	baseline := firstTokenPriorityOrderWithStats(priorityAccountIDs, stats, now, false)
	withProbe := firstTokenPriorityOrderWithStats(priorityAccountIDs, stats, now, true)
	if len(withProbe) == 0 || len(baseline) == 0 || withProbe[0] == baseline[0] {
		return append(baseline, fallbackAccountIDs...)
	}
	claimed, err := cache.TryClaimProbe(ctx, withProbe[0], firstTokenPriorityProbeLease)
	if err != nil || !claimed {
		return append(baseline, fallbackAccountIDs...)
	}
	return append(withProbe, fallbackAccountIDs...)
}

func promoteFirstTokenAccount(accountIDs []int64, accountID int64) []int64 {
	ordered := append([]int64(nil), accountIDs...)
	for index, candidateID := range ordered {
		if candidateID != accountID || index == 0 {
			continue
		}
		copy(ordered[1:index+1], ordered[:index])
		ordered[0] = accountID
		return ordered
	}
	return ordered
}

func firstTokenPriorityOrderWithStats(accountIDs []int64, stats map[int64]FirstTokenLatencyStats, now time.Time, explore bool) []int64 {
	ranked := make([]firstTokenRankedAccount, 0, len(accountIDs))
	allFastKnown := len(accountIDs) > 0
	for index, accountID := range accountIDs {
		stat, found := stats[accountID]
		age := now.Sub(stat.UpdatedAt)
		known := found && firstTokenPriorityStatsReliable(stat) && age >= 0 && age <= firstTokenPriorityFreshFor && stat.PredictedMS > 0
		if !known || stat.PredictedMS > firstTokenPriorityFastThreshold {
			allFastKnown = false
		}
		ranked = append(ranked, firstTokenRankedAccount{id: accountID, stats: stat, known: known, original: index})
	}
	// When every candidate is already reliably below 10 seconds, latency is no
	// longer the scarce signal. Preserve the caller's low-rate/baseline order,
	// except for an alternative account whose adaptive probe interval is due.
	if allFastKnown {
		ordered := append([]int64(nil), accountIDs...)
		if !explore {
			return ordered
		}
		fastestMS := ranked[0].stats.PredictedMS
		for _, item := range ranked[1:] {
			if item.stats.PredictedMS < fastestMS {
				fastestMS = item.stats.PredictedMS
			}
		}
		probeIndex := dynamicFirstTokenProbeIndex(ranked, fastestMS, now)
		if probeIndex <= 0 {
			return ordered
		}
		probe := ordered[probeIndex]
		copy(ordered[1:probeIndex+1], ordered[:probeIndex])
		ordered[0] = probe
		return ordered
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].known != ranked[j].known {
			return ranked[i].known
		}
		left, right := ranked[i].stats.PredictedMS, ranked[j].stats.PredictedMS
		if left > 0 && right > 0 && left != right {
			leftFast := left <= firstTokenPriorityFastThreshold
			rightFast := right <= firstTokenPriorityFastThreshold
			if leftFast != rightFast {
				return leftFast
			}
			delta := left - right
			if delta < 0 {
				delta = -delta
			}
			if delta/minFloat64(left, right) > firstTokenPriorityNearTieRatio {
				return left < right
			}
		}
		return ranked[i].original < ranked[j].original
	})

	if explore {
		fastestMS := 0.0
		if len(ranked) > 0 && ranked[0].known {
			fastestMS = ranked[0].stats.PredictedMS
		}
		probeIndex := dynamicFirstTokenProbeIndex(ranked, fastestMS, now)
		if probeIndex > 0 {
			probe := ranked[probeIndex]
			copy(ranked[1:probeIndex+1], ranked[0:probeIndex])
			ranked[0] = probe
		}
	}
	ordered := make([]int64, 0, len(ranked))
	for _, item := range ranked {
		ordered = append(ordered, item.id)
	}
	return ordered
}

func firstTokenPriorityStatsReliable(stats FirstTokenLatencyStats) bool {
	return stats.SampleCount >= firstTokenPriorityMinimumSamples || (stats.ReliableFast && stats.PredictedMS > 0 && stats.PredictedMS <= firstTokenPriorityFastThreshold)
}

func dynamicFirstTokenProbeIndex(ranked []firstTokenRankedAccount, fastestMS float64, now time.Time) int {
	bestIndex := -1
	bestOverdue := -1.0
	for index := 1; index < len(ranked); index++ {
		item := ranked[index]
		interval := firstTokenPriorityProbeInterval(item.stats, fastestMS)
		age := firstTokenPriorityProbeMax
		if !item.stats.UpdatedAt.IsZero() {
			age = now.Sub(item.stats.UpdatedAt)
		}
		if age < interval {
			continue
		}
		overdue := float64(age) / float64(interval)
		if bestIndex < 0 || overdue > bestOverdue {
			bestIndex = index
			bestOverdue = overdue
		}
	}
	return bestIndex
}

func firstTokenPriorityProbeInterval(stats FirstTokenLatencyStats, fastestMS float64) time.Duration {
	if stats.SampleCount < firstTokenPriorityMinimumSamples {
		return time.Duration(stats.SampleCount+1) * 30 * time.Second
	}
	ratio := 1.0
	if fastestMS > 0 && stats.PredictedMS > fastestMS {
		ratio = stats.PredictedMS / fastestMS
	}
	streak := stats.SlowStreak
	if streak > 8 {
		streak = 8
	}
	streakFactor := 1.0 + float64(streak)*0.75
	interval := time.Duration(float64(firstTokenPriorityProbeBase) * ratio * ratio * streakFactor)
	if interval > firstTokenPriorityProbeMax {
		return firstTokenPriorityProbeMax
	}
	return interval
}

func firstTokenPriorityRanks(ctx context.Context, candidates []*Account, cache FirstTokenLatencyStatsCache) map[int64]int {
	ordered := firstTokenPriorityOrder(ctx, candidates, cache)
	ranks := make(map[int64]int, len(ordered))
	for rank, accountID := range ordered {
		ranks[accountID] = rank
	}
	return ranks
}

func minFloat64(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}
