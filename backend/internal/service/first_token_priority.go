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
		if !found || !firstTokenPriorityStatsFast(stat, now) {
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
	fastKnownIDs := make(map[int64]struct{}, len(accountIDs))
	for index, accountID := range accountIDs {
		stat, found := stats[accountID]
		age := now.Sub(stat.UpdatedAt)
		known := found && firstTokenPriorityStatsReliable(stat) && age >= 0 && age <= firstTokenPriorityFreshFor && stat.PredictedMS > 0
		if stat.FastConfirmationTracked && !stat.ReliableFast && stat.PredictedMS <= firstTokenPriorityFastThreshold {
			known = false
		}
		if !known || !firstTokenPriorityStatsFast(stat, now) {
			allFastKnown = false
		} else {
			fastKnownIDs[accountID] = struct{}{}
		}
		ranked = append(ranked, firstTokenRankedAccount{id: accountID, stats: stat, known: known, original: index})
	}
	// A reliable sub-10-second account is a separate fast pool. Keep the
	// caller's baseline order inside that pool (the scheduler has already
	// applied low-rate ordering), and put slower/unknown accounts behind it.
	// This makes the 10-second rule useful even when one account remains slow.
	if len(fastKnownIDs) > 0 && !allFastKnown {
		fast := make([]int64, 0, len(fastKnownIDs))
		fastRanked := make([]firstTokenRankedAccount, 0, len(fastKnownIDs))
		slow := make([]firstTokenRankedAccount, 0, len(ranked))
		for _, accountID := range accountIDs {
			for _, item := range ranked {
				if item.id == accountID {
					if _, ok := fastKnownIDs[accountID]; ok {
						fast = append(fast, accountID)
						fastRanked = append(fastRanked, item)
					} else {
						slow = append(slow, item)
					}
					break
				}
			}
		}
		// Slow/unknown accounts still use TTFT ordering so the least-bad
		// fallback remains first, while the fast pool retains low-rate order.
		sort.SliceStable(slow, func(i, j int) bool {
			if slow[i].known != slow[j].known {
				return slow[i].known
			}
			if !slow[i].known {
				return slow[i].original < slow[j].original
			}
			left, right := slow[i].stats.PredictedMS, slow[j].stats.PredictedMS
			if left > 0 && right > 0 && left != right {
				return left < right
			}
			return slow[i].original < slow[j].original
		})
		if explore && len(slow) > 0 {
			fastestMS := fastRanked[0].stats.PredictedMS
			for _, item := range fastRanked[1:] {
				if item.stats.PredictedMS < fastestMS {
					fastestMS = item.stats.PredictedMS
				}
			}
			// The first item is a sentinel for the active fast pool. Probe selection
			// scans only the slow/unknown tail, so a higher-rate fast account can
			// never displace the low-rate winner merely because its probe is due.
			probeCandidates := append([]firstTokenRankedAccount{fastRanked[0]}, slow...)
			probeIndex := dynamicFirstTokenProbeIndex(probeCandidates, fastestMS, now)
			if probeIndex > 0 {
				probe := probeCandidates[probeIndex]
				// A due probe is intentionally moved ahead of the fast pool for
				// this request; the shared lease limits it to one gateway.
				return promoteFirstTokenAccount(append(fast, rankedIDs(slow)...), probe.id)
			}
		}
		ordered := append(fast, rankedIDs(slow)...)
		return ordered
	}
	// When every candidate is already reliably below 10 seconds, latency is no
	// longer the scarce signal. Preserve the caller's low-rate/baseline order.
	// Non-selected accounts become probe candidates only after their data ages
	// out of the fast pool, avoiding unnecessary traffic to a higher-rate fast
	// account while still guaranteeing eventual recovery checks.
	if allFastKnown {
		return append([]int64(nil), accountIDs...)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].known != ranked[j].known {
			return ranked[i].known
		}
		if !ranked[i].known {
			return ranked[i].original < ranked[j].original
		}
		left, right := ranked[i].stats.PredictedMS, ranked[j].stats.PredictedMS
		if left > 0 && right > 0 && left != right {
			leftFast := firstTokenPriorityStatsFast(ranked[i].stats, now)
			rightFast := firstTokenPriorityStatsFast(ranked[j].stats, now)
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

func rankedIDs(ranked []firstTokenRankedAccount) []int64 {
	ids := make([]int64, 0, len(ranked))
	for _, item := range ranked {
		ids = append(ids, item.id)
	}
	return ids
}

func firstTokenPriorityStatsReliable(stats FirstTokenLatencyStats) bool {
	return stats.SampleCount >= firstTokenPriorityMinimumSamples || (stats.ReliableFast && stats.PredictedMS > 0 && stats.PredictedMS <= firstTokenPriorityFastThreshold)
}

func firstTokenPriorityStatsFast(stats FirstTokenLatencyStats, now time.Time) bool {
	age := now.Sub(stats.UpdatedAt)
	confirmed := stats.ReliableFast
	if !stats.FastConfirmationTracked {
		confirmed = stats.SampleCount >= firstTokenPriorityMinimumSamples
	}
	return confirmed && age >= 0 && age <= firstTokenPriorityFreshFor && stats.PredictedMS > 0 && stats.PredictedMS <= firstTokenPriorityFastThreshold
}

// applyOpenAIFirstTokenStickyOrder promotes the current session only inside the
// reliable fast pool's minimum effective-rate tier. A slow/unknown item at the
// front is an adaptive probe and must retain its one-request promotion.
func applyOpenAIFirstTokenStickyOrder(
	ctx context.Context,
	ordered []openAIAccountCandidateScore,
	stickyAccountID int64,
	cache FirstTokenLatencyStatsCache,
	rateOrder openAILegacyUpstreamRateOrder,
) {
	if stickyAccountID <= 0 || cache == nil || len(ordered) <= 1 {
		return
	}
	accountIDs := make([]int64, 0, len(ordered))
	stickyIndex := -1
	for index, candidate := range ordered {
		if candidate.account == nil || !isFirstTokenPriorityAccount(candidate.account) {
			continue
		}
		accountIDs = append(accountIDs, candidate.account.ID)
		if candidate.account.ID == stickyAccountID {
			stickyIndex = index
		}
	}
	if stickyIndex <= 0 || len(accountIDs) == 0 {
		return
	}
	stats, err := cache.GetStatsBatch(ctx, accountIDs)
	if err != nil {
		return
	}
	now := time.Now()
	first := ordered[0].account
	sticky := ordered[stickyIndex].account
	if first == nil || sticky == nil || !firstTokenPriorityStatsFast(stats[first.ID], now) || !firstTokenPriorityStatsFast(stats[sticky.ID], now) {
		return
	}
	for _, candidate := range ordered {
		if candidate.account == nil || !firstTokenPriorityStatsFast(stats[candidate.account.ID], now) {
			continue
		}
		if rateOrder.compare(candidate.account, sticky) < 0 {
			return
		}
	}
	stickyCandidate := ordered[stickyIndex]
	copy(ordered[1:stickyIndex+1], ordered[:stickyIndex])
	ordered[0] = stickyCandidate
}

func (s *defaultOpenAIAccountScheduler) shouldPreserveFirstTokenStickyBinding(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	selectedAccountID int64,
	ordered []openAIAccountCandidateScore,
) bool {
	if !req.FirstTokenPriority || req.StickyAccountID <= 0 || selectedAccountID <= 0 || selectedAccountID == req.StickyAccountID || s == nil || s.service == nil || s.service.rateLimitService == nil {
		return false
	}
	cache := s.service.rateLimitService.firstTokenLatencyStatsCache
	if cache == nil {
		return false
	}
	accounts := make([]*Account, 0, len(ordered))
	accountIDs := make([]int64, 0, len(ordered))
	var sticky *Account
	for _, candidate := range ordered {
		if candidate.account == nil || !isFirstTokenPriorityAccount(candidate.account) {
			continue
		}
		accounts = append(accounts, candidate.account)
		accountIDs = append(accountIDs, candidate.account.ID)
		if candidate.account.ID == req.StickyAccountID {
			sticky = candidate.account
		}
	}
	if sticky == nil {
		return false
	}
	stats, err := cache.GetStatsBatch(ctx, accountIDs)
	now := time.Now()
	if err != nil || !firstTokenPriorityStatsFast(stats[sticky.ID], now) {
		return false
	}
	if !firstTokenPriorityStatsFast(stats[selectedAccountID], now) {
		return true
	}
	if !req.UseUpstreamTokenCost {
		return true
	}
	rateOrder := newOpenAILegacyUpstreamRateOrder(accounts, now, s.service.openAIOAuthSchedulingRateMultiplier(ctx))
	for _, account := range accounts {
		if firstTokenPriorityStatsFast(stats[account.ID], now) && rateOrder.compare(account, sticky) < 0 {
			return false
		}
	}
	return true
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
	if stats.RecoveryFastStreak > 0 {
		return firstTokenPriorityProbeBase
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
