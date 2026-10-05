package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const intelligencePauseUntilKey = "intelligence_pause_until"
const intelligenceAllowedGroupsKey = "intelligence_allowed_groups"
const intelligenceRecoveryRequiredKey = "intelligence_recovery_required"
const intelligenceProtectedGroupsKey = "intelligence_protected_groups"

// Exported for repository-owned account-group mutations. Group membership
// changes must release any stale intelligence isolation immediately.
const (
	IntelligencePauseUntilExtraKey       = intelligencePauseUntilKey
	IntelligenceAllowedGroupsExtraKey    = intelligenceAllowedGroupsKey
	IntelligenceRecoveryRequiredExtraKey = intelligenceRecoveryRequiredKey
	IntelligenceProtectedGroupsExtraKey  = intelligenceProtectedGroupsKey
)

type IntelligencePoolResetter interface {
	ResetForIntelligencePause(context.Context, int64, time.Time) error
}
type intelligencePoolRecoverer interface {
	ClearIntelligencePause(context.Context, int64) error
}

func IntelligenceProtectionGroupEligible(group *Group) bool {
	return group != nil && group.Platform == PlatformOpenAI && group.Status == StatusActive && strings.Contains(group.Name, "不降智")
}

func intelligenceAllowedGroups(account *Account) []int64 {
	if account == nil {
		return nil
	}
	var groups []int64
	switch values := account.Extra[intelligenceAllowedGroupsKey].(type) {
	case []int64:
		groups = values
	case []any:
		for _, value := range values {
			switch id := value.(type) {
			case float64:
				groups = append(groups, int64(id))
			case int64:
				groups = append(groups, id)
			case int:
				groups = append(groups, int64(id))
			}
		}
	}
	return groups
}

func intelligenceIsolationActive(account *Account, now time.Time) bool {
	if account == nil {
		return false
	}
	until, _ := time.Parse(time.RFC3339Nano, account.GetExtraString(intelligencePauseUntilKey))
	// A last-account exception supplies ongoing checks. Keep other groups isolated
	// until a normal result, instead of returning a known degraded account on TTL.
	recovery, _ := account.Extra[intelligenceRecoveryRequiredKey].(bool)
	return until.After(now) || recovery
}

func intelligenceAccountBlocked(account *Account, groupID *int64, now time.Time) bool {
	if !intelligenceIsolationActive(account, now) {
		return false
	}
	if groupID != nil {
		if protected, present := account.Extra[intelligenceProtectedGroupsKey]; present {
			protectedIDs := intelligenceGroupIDList(protected)
			if !containsInt64(protectedIDs, *groupID) {
				return false
			}
		}
		for _, id := range intelligenceAllowedGroups(account) {
			if id == *groupID {
				return false
			}
		}
	}
	return true
}

func (s *OpenAIGatewayService) intelligenceAccountBlockedForGroup(ctx context.Context, account *Account, groupID *int64, now time.Time) bool {
	if account == nil || groupID == nil || !intelligenceIsolationActive(account, now) {
		return false
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil && group.ID == *groupID {
		return IntelligenceProtectionGroupEligible(group) && intelligenceAccountBlocked(account, groupID, now)
	}
	if s != nil && s.schedulerSnapshot != nil {
		if group, err := s.schedulerSnapshot.GetGroupByIDLite(ctx, *groupID); err == nil && group != nil {
			return IntelligenceProtectionGroupEligible(group) && intelligenceAccountBlocked(account, groupID, now)
		}
	}
	for _, group := range account.Groups {
		if group != nil && group.ID == *groupID {
			return IntelligenceProtectionGroupEligible(group) && intelligenceAccountBlocked(account, groupID, now)
		}
	}
	for _, membership := range account.AccountGroups {
		if membership.GroupID == *groupID && membership.Group != nil {
			return IntelligenceProtectionGroupEligible(membership.Group) && intelligenceAccountBlocked(account, groupID, now)
		}
	}
	return intelligenceAccountBlocked(account, groupID, now)
}

func intelligenceGroupIDList(value any) []int64 {
	var result []int64
	switch values := value.(type) {
	case []int64:
		return values
	case []any:
		for _, value := range values {
			switch id := value.(type) {
			case float64:
				result = append(result, int64(id))
			case int64:
				result = append(result, id)
			case int:
				result = append(result, int64(id))
			}
		}
	}
	return result
}

// ProtectDegradedIntelligenceAccount runs under the global result lock. The
// account's streak is shared, but sole-account exceptions are group-specific.
func (s *OpenAIGatewayService) ProtectDegradedIntelligenceAccount(ctx context.Context, group *Group, accountID int64, model string, userID int64) (bool, error) {
	if s == nil || !IntelligenceProtectionGroupEligible(group) || !s.isFirstTokenPriorityEnabled(ctx) {
		return false, nil
	}
	if s.rateLimitService == nil || s.accountRepo == nil {
		return false, errors.New("intelligence protection dependencies unavailable")
	}
	resetter, ok := s.rateLimitService.firstTokenLatencyStatsCache.(IntelligencePoolResetter)
	if !ok {
		return false, errors.New("intelligence pool reset unavailable")
	}
	target, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return false, err
	}
	if target == nil {
		return false, nil
	}
	legacyReason := target.TempUnschedulableReason
	legacyPause := strings.HasPrefix(legacyReason, "intelligence:")
	if legacyPause {
		copy := *target
		copy.TempUnschedulableUntil = nil
		target = &copy
	}
	if !isFirstTokenPriorityAccount(target) || !target.IsSchedulable() {
		return false, nil
	}
	memberships, ok := s.accountRepo.(interface {
		GetGroups(context.Context, int64) ([]Group, error)
	})
	if !ok {
		return false, errors.New("intelligence account memberships unavailable")
	}
	groups, err := memberships.GetGroups(ctx, accountID)
	if err != nil {
		return false, err
	}
	now := time.Now()
	allowed := []int64{}
	protected := []int64{}
	restricted := 0
	for i := range groups {
		candidateGroup := &groups[i]
		if !IntelligenceProtectionGroupEligible(candidateGroup) {
			continue
		}
		protected = append(protected, candidateGroup.ID)
		alternative, err := s.intelligenceGroupHasAlternative(ctx, candidateGroup, accountID, model, userID)
		if err != nil {
			return false, err
		}
		if alternative {
			restricted++
		} else {
			allowed = append(allowed, candidateGroup.ID)
		}
	}
	if restricted == 0 && !intelligenceIsolationActive(target, now) {
		if legacyPause {
			return false, s.clearLegacyIntelligencePause(ctx, accountID, legacyReason)
		}
		return false, nil
	}
	sort.Slice(allowed, func(i, j int) bool { return allowed[i] < allowed[j] })
	until := now.Add(20 * time.Minute)
	active := intelligenceIsolationActive(target, now)
	if active {
		if previous, err := time.Parse(time.RFC3339Nano, target.GetExtraString(intelligencePauseUntilKey)); err == nil {
			until = previous
		}
	} else if err := resetter.ResetForIntelligencePause(ctx, accountID, until); err != nil {
		return false, err
	}
	// Extra updates atomically merge only these keys and invalidate shared
	// scheduler snapshots. Never remove memberships or globally disable the account.
	err = s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		intelligencePauseUntilKey:       until.Format(time.RFC3339Nano),
		intelligenceAllowedGroupsKey:    allowed,
		intelligenceRecoveryRequiredKey: len(allowed) > 0,
		intelligenceProtectedGroupsKey:  protected,
	})
	if err == nil && legacyPause {
		err = s.clearLegacyIntelligencePause(ctx, accountID, legacyReason)
	}
	return err == nil, err
}

func (s *OpenAIGatewayService) intelligenceGroupHasAlternative(ctx context.Context, group *Group, accountID int64, model string, userID int64) (bool, error) {
	accounts, err := s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, PlatformOpenAI)
	if err != nil {
		return false, err
	}
	ctx = context.WithValue(ctx, ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.UserID, userID)
	ctx = s.withOpenAIProfitControlGate(ctx, &group.ID)
	req := OpenAIAccountScheduleRequest{GroupID: &group.ID, Platform: PlatformOpenAI, RequestedModel: model, FirstTokenPriority: true, MinCacheRate: group.MinCacheRate, RequirePrivacySet: group.RequirePrivacySet}
	scheduler := &defaultOpenAIAccountScheduler{service: s}
	scheduler.warmGroupCacheRateStats(ctx, accounts, req)
	thresholds := s.rateLimitService.settingService.GetAccountSchedulingThresholds(ctx)
	for i := range accounts {
		account := &accounts[i]
		if account.ID != accountID && account.Platform == PlatformOpenAI && account.IsSchedulable() &&
			!EvaluateAccountSchedulingThreshold(account, thresholds, time.Now()).ShouldPause && scheduler.isAccountRequestCompatible(ctx, account, req) {
			return true, nil
		}
	}
	return false, nil
}

// A single normal attributed check in any eligible group clears the shared
// intelligence restriction immediately, without undoing other cooldowns.
func (s *OpenAIGatewayService) RecoverIntelligenceAccount(ctx context.Context, accountID int64) error {
	if s == nil || s.accountRepo == nil {
		return errors.New("intelligence recovery dependencies unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account == nil {
		return nil
	}
	legacy := strings.HasPrefix(account.TempUnschedulableReason, "intelligence:")
	if !legacy && account.GetExtraString(intelligencePauseUntilKey) == "" {
		return nil
	}
	if s.rateLimitService == nil {
		return errors.New("intelligence recovery cache unavailable")
	}
	cache, ok := s.rateLimitService.firstTokenLatencyStatsCache.(intelligencePoolRecoverer)
	if !ok {
		return errors.New("intelligence recovery cache unavailable")
	}
	if err := cache.ClearIntelligencePause(ctx, accountID); err != nil {
		return err
	}
	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{intelligencePauseUntilKey: nil, intelligenceAllowedGroupsKey: nil, intelligenceRecoveryRequiredKey: nil, intelligenceProtectedGroupsKey: nil}); err != nil {
		return err
	}
	if legacy {
		return s.clearLegacyIntelligencePause(ctx, accountID, account.TempUnschedulableReason)
	}
	return nil
}

func (s *OpenAIGatewayService) clearLegacyIntelligencePause(ctx context.Context, id int64, reason string) error {
	repo, ok := s.accountRepo.(interface {
		ClearIntelligenceTempUnschedulable(context.Context, int64, string) error
	})
	if !ok {
		return errors.New("legacy intelligence pause recovery unavailable")
	}
	return repo.ClearIntelligenceTempUnschedulable(ctx, id, reason)
}
