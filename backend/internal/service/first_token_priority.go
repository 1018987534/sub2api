package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const (
	firstTokenPriorityMinimumSamples  = 3
	firstTokenPriorityFreshFor        = 20 * time.Minute
	firstTokenPriorityNearTieRatio    = 0.20
	firstTokenPriorityNearTieFloor    = 2_000.0
	firstTokenPriorityFastThreshold   = 10_000.0
	firstTokenPriorityStickyThreshold = 15_000.0
	firstTokenPriorityProbeBase       = 2 * time.Minute
	firstTokenPriorityRecoveryProbe   = 30 * time.Second
	firstTokenPriorityProbeMax        = 6 * time.Hour
	firstTokenPriorityProbeLease      = 10 * time.Minute
	firstTokenPriorityManualProbeTTL  = 10 * time.Minute
	firstTokenLatencyHiddenAccount    = "plus-xiaobaishu 生图"
	firstTokenLatencyHiddenGroup      = "PRO 监控专用"
)

var (
	ErrFirstTokenManualProbeUnavailable = errors.New("first-token manual probe is unavailable")
	ErrFirstTokenManualProbeIneligible  = errors.New("account is not eligible for first-token probing")
)

type firstTokenRankedAccount struct {
	id       int64
	stats    FirstTokenLatencyStats
	known    bool
	original int
}

type firstTokenProbeEligibilityContextKey struct{}

// WithFirstTokenProbeEligibility marks whether the current request can produce
// a first_token_ms sample. Non-stream requests must not consume the shared
// probe lease because they cannot advance recovery confirmation.
func WithFirstTokenProbeEligibility(ctx context.Context, eligible bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, firstTokenProbeEligibilityContextKey{}, eligible)
}

func firstTokenProbeEligible(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	eligible, ok := ctx.Value(firstTokenProbeEligibilityContextKey{}).(bool)
	return ok && eligible
}

type AccountFirstTokenLatencyMetric struct {
	AccountID                int64                           `json:"account_id"`
	AccountName              string                          `json:"account_name"`
	PredictedMS              float64                         `json:"predicted_ms"`
	HasPrediction            bool                            `json:"has_prediction"`
	IsFastPool               bool                            `json:"is_fast_pool"`
	SchedulingRateMultiplier *float64                        `json:"scheduling_rate_multiplier"`
	Groups                   []AccountFirstTokenLatencyGroup `json:"groups"`
	SampleCount              int64                           `json:"sample_count"`
	UpdatedAt                time.Time                       `json:"updated_at"`
	SlowStreak               int                             `json:"slow_streak"`
	RecoveryFastStreak       int                             `json:"recovery_fast_streak"`
	ProbeIntervalSeconds     int64                           `json:"probe_interval_seconds"`
	CacheRate                *float64                        `json:"cache_rate"`
	CacheReadTokens          int64                           `json:"cache_read_tokens"`
	CacheRateDenominator     int64                           `json:"cache_rate_denominator"`
}

type AccountFirstTokenLatencyGroup struct {
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
}

// AccountCacheStats is the rolling cache-token aggregate used alongside the
// account scheduling view. The denominator follows the channel monitor
// convention: uncached input plus cache creation and cache reads.
type AccountCacheStats struct {
	CacheReadTokens      int64
	CacheRateDenominator int64
}

// AccountCacheStatsProvider is optional so existing test doubles and other
// UsageLogRepository implementations remain compatible.
type AccountCacheStatsProvider interface {
	GetAccountCacheStatsBatch(ctx context.Context, accountIDs []int64, startTime, endTime time.Time) (map[int64]AccountCacheStats, error)
}

// FirstTokenManualProbeCache adds an administrator-requested probe queue on top
// of the normal adaptive cadence without widening the hot-path cache contract.
type FirstTokenManualProbeCache interface {
	RequestManualProbe(ctx context.Context, accountID int64, ttl time.Duration) error
	TryClaimManualProbe(ctx context.Context, accountIDs []int64, lease time.Duration) (int64, bool, error)
}

func isFirstTokenPriorityAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey && account.IsActive() && account.Schedulable
}

func (s *RateLimitService) AccountFirstTokenLatencyMetrics(ctx context.Context, accounts []Account) ([]AccountFirstTokenLatencyMetric, error) {
	if s == nil || s.firstTokenLatencyStatsCache == nil || len(accounts) == 0 {
		return []AccountFirstTokenLatencyMetric{}, nil
	}
	now := time.Now()
	eligible := make([]Account, 0, len(accounts))
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if !isFirstTokenPriorityAccount(&account) || !account.IsSchedulableAt(now) {
			continue
		}
		eligible = append(eligible, account)
		accountIDs = append(accountIDs, account.ID)
	}
	stats, err := s.firstTokenLatencyStatsCache.GetStatsBatch(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	cacheStats := make(map[int64]AccountCacheStats)
	if provider, ok := s.usageRepo.(AccountCacheStatsProvider); ok {
		cacheStats, err = provider.GetAccountCacheStatsBatch(ctx, accountIDs, now.Add(-24*time.Hour), now)
		if err != nil {
			return nil, err
		}
	}
	fastestMS := 0.0
	for _, stat := range stats {
		if stat.PredictedMS > 0 && (fastestMS == 0 || stat.PredictedMS < fastestMS) {
			fastestMS = stat.PredictedMS
		}
	}
	metrics := make([]AccountFirstTokenLatencyMetric, 0, len(eligible))
	for _, account := range eligible {
		if strings.EqualFold(strings.TrimSpace(account.Name), firstTokenLatencyHiddenAccount) {
			continue
		}
		groups, belongsToSchedulableGroup := firstTokenLatencyMetricGroups(&account, now)
		if !belongsToSchedulableGroup {
			continue
		}
		stat, ok := stats[account.ID]
		hasPrediction := ok && stat.PredictedMS > 0
		var schedulingRateMultiplier *float64
		if rate, found := openAIFreshUpstreamBillingRate(&account, now); found {
			schedulingRateMultiplier = &rate
		}
		accountCache := cacheStats[account.ID]
		var cacheRate *float64
		if accountCache.CacheRateDenominator > 0 {
			rate := float64(accountCache.CacheReadTokens) / float64(accountCache.CacheRateDenominator)
			cacheRate = &rate
		}
		metrics = append(metrics, AccountFirstTokenLatencyMetric{
			AccountID:                account.ID,
			AccountName:              account.Name,
			PredictedMS:              stat.PredictedMS,
			HasPrediction:            hasPrediction,
			IsFastPool:               firstTokenPriorityStatsFast(stat, now),
			SchedulingRateMultiplier: schedulingRateMultiplier,
			Groups:                   groups,
			SampleCount:              stat.SampleCount,
			UpdatedAt:                stat.UpdatedAt,
			SlowStreak:               stat.SlowStreak,
			RecoveryFastStreak:       stat.RecoveryFastStreak,
			ProbeIntervalSeconds:     int64(firstTokenPriorityProbeInterval(stat, fastestMS).Seconds()),
			CacheRate:                cacheRate,
			CacheReadTokens:          accountCache.CacheReadTokens,
			CacheRateDenominator:     accountCache.CacheRateDenominator,
		})
	}
	sort.SliceStable(metrics, func(i, j int) bool {
		if metrics[i].HasPrediction != metrics[j].HasPrediction {
			return metrics[i].HasPrediction
		}
		if metrics[i].PredictedMS == metrics[j].PredictedMS {
			return metrics[i].AccountID < metrics[j].AccountID
		}
		return metrics[i].PredictedMS < metrics[j].PredictedMS
	})
	return metrics, nil
}

func firstTokenLatencyMetricGroups(account *Account, now time.Time) ([]AccountFirstTokenLatencyGroup, bool) {
	if account == nil {
		return []AccountFirstTokenLatencyGroup{}, false
	}
	groupByID := make(map[int64]*Group, len(account.Groups)+len(account.AccountGroups))
	membershipIDs := make(map[int64]struct{}, len(account.GroupIDs)+len(account.AccountGroups))
	includeGroup := func(group *Group) bool {
		if group == nil ||
			strings.EqualFold(strings.TrimSpace(group.Name), firstTokenLatencyHiddenGroup) ||
			!((group.Status == "" || group.Status == StatusActive) &&
				(group.Platform == "" || group.Platform == PlatformOpenAI)) {
			return false
		}
		reports := PreviewProfitAdmission([]ProfitPreviewGroupInput{{
			Group:    group,
			Accounts: []*Account{account},
		}}, now)
		return len(reports) == 1 && len(reports[0].Verdicts) == 1 &&
			reports[0].Verdicts[0].Class == ProfitPreviewClassAdmitted
	}
	for _, group := range account.Groups {
		if group == nil || group.ID <= 0 {
			continue
		}
		membershipIDs[group.ID] = struct{}{}
		groupByID[group.ID] = group
	}
	for _, accountGroup := range account.AccountGroups {
		if accountGroup.GroupID <= 0 {
			continue
		}
		membershipIDs[accountGroup.GroupID] = struct{}{}
		if accountGroup.Group != nil {
			groupByID[accountGroup.GroupID] = accountGroup.Group
		}
	}
	for _, groupID := range account.GroupIDs {
		if groupID > 0 {
			membershipIDs[groupID] = struct{}{}
		}
	}
	if len(membershipIDs) == 0 {
		return []AccountFirstTokenLatencyGroup{}, true
	}
	groups := make([]AccountFirstTokenLatencyGroup, 0, len(membershipIDs))
	for groupID := range membershipIDs {
		group := groupByID[groupID]
		if includeGroup(group) {
			groups = append(groups, AccountFirstTokenLatencyGroup{GroupID: groupID, GroupName: group.Name})
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GroupID < groups[j].GroupID })
	return groups, len(groups) > 0
}

func (s *RateLimitService) ObserveFirstTokenLatency(ctx context.Context, account *Account, requestID string, firstTokenMs *int) {
	if s == nil || s.firstTokenLatencyStatsCache == nil || !isFirstTokenPriorityAccount(account) || firstTokenMs == nil || account.ID <= 0 || *firstTokenMs <= 0 {
		return
	}
	if err := s.firstTokenLatencyStatsCache.RecordSample(ctx, account.ID, requestID, *firstTokenMs); err != nil {
		slog.Warn("first_token_latency_stats_record_failed", "account_id", account.ID, "error", err)
	}
}

// RequestFirstTokenManualProbe queues the account for the next eligible real
// streaming request. It does not synthesize a billable upstream request.
func (s *RateLimitService) RequestFirstTokenManualProbe(ctx context.Context, account *Account) error {
	if !isFirstTokenPriorityAccount(account) || !account.IsSchedulableAt(time.Now()) || account.ID <= 0 {
		return ErrFirstTokenManualProbeIneligible
	}
	cache, ok := s.firstTokenLatencyStatsCache.(FirstTokenManualProbeCache)
	if !ok || cache == nil {
		return ErrFirstTokenManualProbeUnavailable
	}
	if err := cache.RequestManualProbe(ctx, account.ID, firstTokenPriorityManualProbeTTL); err != nil {
		return fmt.Errorf("request first-token manual probe: %w", err)
	}
	return nil
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
	return firstTokenPriorityOrderWithProbe(ctx, candidates, cache, true)
}

func firstTokenPriorityOrderWithProbe(ctx context.Context, candidates []*Account, cache FirstTokenLatencyStatsCache, allowProbe bool) []int64 {
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
	baseline := firstTokenPriorityOrderWithStats(priorityAccountIDs, stats, now, false)
	ordered := append(baseline, fallbackAccountIDs...)
	if allowProbe {
		if manualCache, ok := cache.(FirstTokenManualProbeCache); ok {
			manualAccountID, claimed, claimErr := manualCache.TryClaimManualProbe(ctx, priorityAccountIDs, firstTokenPriorityProbeLease)
			if claimErr == nil && claimed {
				if allPriorityAccountsFast {
					return promoteFirstTokenAccount(accountIDs, manualAccountID)
				}
				return promoteFirstTokenAccount(ordered, manualAccountID)
			}
		}
	}
	if allPriorityAccountsFast {
		return accountIDs
	}
	if !allowProbe {
		return ordered
	}
	for _, probeAccountID := range firstTokenPriorityProbeAccountIDs(priorityAccountIDs, stats, now) {
		claimed, err := cache.TryClaimProbe(ctx, probeAccountID, firstTokenPriorityProbeLease)
		if err != nil {
			return ordered
		}
		if claimed {
			return promoteFirstTokenAccount(ordered, probeAccountID)
		}
	}
	return ordered
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
		slow = stableFirstTokenRankedOrder(slow, now)
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

	ranked = stableFirstTokenRankedOrder(ranked, now)

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

func firstTokenPriorityProbeAccountIDs(accountIDs []int64, stats map[int64]FirstTokenLatencyStats, now time.Time) []int64 {
	if len(accountIDs) <= 1 {
		return nil
	}
	baseline := firstTokenPriorityOrderWithStats(accountIDs, stats, now, false)
	fastIDs := make(map[int64]struct{}, len(accountIDs))
	fastestMS := 0.0
	for _, accountID := range accountIDs {
		stat, found := stats[accountID]
		if !found || !firstTokenPriorityStatsFast(stat, now) {
			continue
		}
		fastIDs[accountID] = struct{}{}
		if fastestMS == 0 || stat.PredictedMS < fastestMS {
			fastestMS = stat.PredictedMS
		}
	}
	if len(fastIDs) == len(accountIDs) {
		return nil
	}

	probeRanked := make([]firstTokenRankedAccount, 0, len(baseline)+1)
	if len(fastIDs) > 0 {
		for _, accountID := range baseline {
			if _, fast := fastIDs[accountID]; fast {
				if len(probeRanked) == 0 {
					probeRanked = append(probeRanked, firstTokenRankedAccount{id: accountID, stats: stats[accountID], known: true})
				}
				continue
			}
			probeRanked = append(probeRanked, firstTokenRankedAccount{id: accountID, stats: stats[accountID]})
		}
	} else {
		for index, accountID := range baseline {
			stat, found := stats[accountID]
			age := now.Sub(stat.UpdatedAt)
			known := found && firstTokenPriorityStatsReliable(stat) && age >= 0 && age <= firstTokenPriorityFreshFor && stat.PredictedMS > 0
			if stat.FastConfirmationTracked && !stat.ReliableFast && stat.PredictedMS <= firstTokenPriorityFastThreshold {
				known = false
			}
			probeRanked = append(probeRanked, firstTokenRankedAccount{id: accountID, stats: stat, known: known, original: index})
		}
		if len(probeRanked) > 0 && probeRanked[0].known {
			fastestMS = probeRanked[0].stats.PredictedMS
		}
	}

	indexes := dynamicFirstTokenProbeIndexes(probeRanked, fastestMS, now)
	result := make([]int64, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, probeRanked[index].id)
	}
	return result
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
	if stats.CircuitBroken {
		return false
	}
	age := now.Sub(stats.UpdatedAt)
	confirmed := stats.ReliableFast
	if !stats.FastConfirmationTracked {
		confirmed = stats.SampleCount >= firstTokenPriorityMinimumSamples
	}
	return confirmed && age >= 0 && age <= firstTokenPriorityFreshFor && stats.PredictedMS > 0 && stats.PredictedMS <= firstTokenPriorityFastThreshold
}

// firstTokenPriorityDefaultStickyEligible keeps the legacy hard session
// affinity only while the shared, stable TTFT signal remains healthy. Unknown
// or stale data falls back to the adaptive weighted policy instead of pinning
// a session to an account whose current latency cannot be trusted.
func firstTokenPriorityDefaultStickyEligible(stats FirstTokenLatencyStats, now time.Time) bool {
	if stats.CircuitBroken {
		return false
	}
	age := now.Sub(stats.UpdatedAt)
	return firstTokenPriorityStatsReliable(stats) &&
		age >= 0 && age <= firstTokenPriorityFreshFor &&
		stats.PredictedMS > 0 && stats.PredictedMS <= firstTokenPriorityStickyThreshold
}

// firstTokenPriorityClearlyFaster is the ranking hysteresis gate. Crossing the
// fast-pool boundary always wins; inside the slow pool, minor prediction
// movement does not reshuffle accounts.
func firstTokenPriorityClearlyFaster(left, right FirstTokenLatencyStats, now time.Time) bool {
	leftFast := firstTokenPriorityStatsFast(left, now)
	rightFast := firstTokenPriorityStatsFast(right, now)
	if leftFast != rightFast {
		return leftFast
	}
	if left.PredictedMS <= 0 || right.PredictedMS <= 0 || left.PredictedMS >= right.PredictedMS {
		return false
	}
	delta := right.PredictedMS - left.PredictedMS
	return delta >= firstTokenPriorityNearTieFloor && delta/right.PredictedMS >= firstTokenPriorityNearTieRatio
}

// stableFirstTokenRankedOrder uses account ID as a deterministic tie baseline,
// rather than inheriting rate or session-sticky ordering from the caller. A
// challenger replaces the current leader only after clearing the hysteresis
// gate, so slow-pool ordering remains TTFT-only and stable across requests.
func stableFirstTokenRankedOrder(ranked []firstTokenRankedAccount, now time.Time) []firstTokenRankedAccount {
	known := make([]firstTokenRankedAccount, 0, len(ranked))
	unknown := make([]firstTokenRankedAccount, 0, len(ranked))
	for _, item := range ranked {
		if item.known {
			known = append(known, item)
		} else {
			unknown = append(unknown, item)
		}
	}
	sort.Slice(known, func(i, j int) bool { return known[i].id < known[j].id })
	ordered := make([]firstTokenRankedAccount, 0, len(ranked))
	for len(known) > 0 {
		winner := 0
		for index := 1; index < len(known); index++ {
			if firstTokenPriorityClearlyFaster(known[index].stats, known[winner].stats, now) {
				winner = index
			}
		}
		ordered = append(ordered, known[winner])
		known = append(known[:winner], known[winner+1:]...)
	}
	return append(ordered, unknown...)
}

// applyOpenAIFirstTokenStickyOrder reuses the legacy low-rate weighted sticky
// policy inside the current TTFT pool. A reliable account above the 15-second
// hard-sticky threshold may receive weighted affinity in the slow pool, but it
// never crosses a rate tier or lets a slow sticky account override the fast pool.
func applyOpenAIFirstTokenStickyOrder(
	ctx context.Context,
	ordered []openAIAccountCandidateScore,
	req OpenAIAccountScheduleRequest,
	cache FirstTokenLatencyStatsCache,
	rateOrder openAILegacyUpstreamRateOrder,
) {
	if req.StickyAccountID <= 0 || cache == nil || len(ordered) <= 1 {
		return
	}
	accountIDs := make([]int64, 0, len(ordered))
	for _, candidate := range ordered {
		if candidate.account == nil || !isFirstTokenPriorityAccount(candidate.account) {
			continue
		}
		accountIDs = append(accountIDs, candidate.account.ID)
	}
	if len(accountIDs) == 0 {
		return
	}
	stats, err := cache.GetStatsBatch(ctx, accountIDs)
	if err != nil {
		return
	}
	now := time.Now()
	stickyStats, stickyFound := stats[req.StickyAccountID]
	if !stickyFound || !firstTokenPriorityStatsReliable(stickyStats) {
		return
	}
	stickyAge := now.Sub(stickyStats.UpdatedAt)
	if stickyAge < 0 || stickyAge > firstTokenPriorityFreshFor || stickyStats.PredictedMS <= firstTokenPriorityStickyThreshold {
		return
	}
	applyOpenAILegacySoftStickyOrder(
		ordered,
		func(candidate openAIAccountCandidateScore) *Account { return candidate.account },
		rateOrder,
		openAILegacySoftStickyPolicy{
			enabled:   true,
			accountID: req.StickyAccountID,
			weight:    openAILegacySessionStickyWeight,
			seed:      deriveOpenAISelectionSeed(req),
		},
		func(account *Account) int {
			if account != nil && firstTokenPriorityStatsFast(stats[account.ID], now) {
				return 1
			}
			return 0
		},
	)
}

func dynamicFirstTokenProbeIndex(ranked []firstTokenRankedAccount, fastestMS float64, now time.Time) int {
	indexes := dynamicFirstTokenProbeIndexes(ranked, fastestMS, now)
	if len(indexes) == 0 {
		return -1
	}
	return indexes[0]
}

func dynamicFirstTokenProbeIndexes(ranked []firstTokenRankedAccount, fastestMS float64, now time.Time) []int {
	type dueProbe struct {
		index          int
		recoveryStreak int
		overdue        float64
	}
	due := make([]dueProbe, 0, len(ranked))
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
		due = append(due, dueProbe{
			index:          index,
			recoveryStreak: item.stats.RecoveryFastStreak,
			overdue:        float64(age) / float64(interval),
		})
	}
	sort.SliceStable(due, func(i, j int) bool {
		leftRecovering := due[i].recoveryStreak > 0
		rightRecovering := due[j].recoveryStreak > 0
		if leftRecovering != rightRecovering {
			return leftRecovering
		}
		if due[i].recoveryStreak != due[j].recoveryStreak {
			return due[i].recoveryStreak > due[j].recoveryStreak
		}
		return due[i].overdue > due[j].overdue
	})
	indexes := make([]int, 0, len(due))
	for _, probe := range due {
		indexes = append(indexes, probe.index)
	}
	return indexes
}

func firstTokenPriorityProbeInterval(stats FirstTokenLatencyStats, fastestMS float64) time.Duration {
	if stats.CircuitBroken || stats.RecoveryFastStreak > 0 {
		return firstTokenPriorityRecoveryProbe
	}
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

func firstTokenPriorityRanks(ctx context.Context, candidates []*Account, cache FirstTokenLatencyStatsCache, allowProbe bool) map[int64]int {
	ordered := firstTokenPriorityOrderWithProbe(ctx, candidates, cache, allowProbe)
	ranks := make(map[int64]int, len(ordered))
	for rank, accountID := range ordered {
		ranks[accountID] = rank
	}
	return ranks
}
