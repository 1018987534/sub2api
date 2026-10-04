package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type IntelligencePoolResetter interface {
	ResetForIntelligencePause(context.Context, int64, time.Time) error
}

// ProtectDegradedIntelligenceAccount is called under the shared database
// protection lock, after two attributed degraded results for this account.
func (s *OpenAIGatewayService) ProtectDegradedIntelligenceAccount(ctx context.Context, group *Group, accountID int64, model string, userID int64) (bool, error) {
	if s == nil || group == nil || group.Platform != PlatformOpenAI || group.Status != StatusActive ||
		!strings.Contains(group.Name, "不降智") || !s.isFirstTokenPriorityEnabled(ctx) {
		return false, nil
	}
	if s.rateLimitService == nil || s.accountRepo == nil {
		return false, errors.New("intelligence protection dependencies unavailable")
	}
	resetter, ok := s.rateLimitService.firstTokenLatencyStatsCache.(IntelligencePoolResetter)
	if !ok {
		return false, errors.New("intelligence pool reset unavailable")
	}
	// Read current database state rather than a potentially stale fleet snapshot.
	accounts, err := s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, PlatformOpenAI)
	if err != nil {
		return false, err
	}
	ctx = context.WithValue(ctx, ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.UserID, userID)
	ctx = s.withOpenAIProfitControlGate(ctx, &group.ID)
	req := OpenAIAccountScheduleRequest{GroupID: &group.ID, Platform: PlatformOpenAI, RequestedModel: model,
		FirstTokenPriority: true, MinCacheRate: group.MinCacheRate, RequirePrivacySet: group.RequirePrivacySet}
	scheduler := &defaultOpenAIAccountScheduler{service: s}
	scheduler.warmGroupCacheRateStats(ctx, accounts, req)
	thresholds := s.rateLimitService.settingService.GetAccountSchedulingThresholds(ctx)
	var target *Account
	alternative := false
	for i := range accounts {
		account := &accounts[i]
		if account.ID == accountID {
			target = account
			continue
		}
		if account.Platform == PlatformOpenAI && account.IsSchedulable() &&
			!EvaluateAccountSchedulingThreshold(account, thresholds, time.Now()).ShouldPause &&
			scheduler.isAccountRequestCompatible(ctx, account, req) {
			alternative = true
		}
	}
	if !alternative || !isFirstTokenPriorityAccount(target) || !target.IsSchedulable() {
		return false, nil
	}
	until := time.Now().Add(20 * time.Minute)
	// Reset first so a database/cache failure never pauses an account with its
	// old fast-pool score. Late in-flight samples are suppressed until expiry.
	if err := resetter.ResetForIntelligencePause(ctx, accountID, until); err != nil {
		return false, err
	}
	if err := s.accountRepo.SetTempUnschedulable(ctx, accountID, until,
		fmt.Sprintf("intelligence: group %d consecutive degraded checks; paused 20 minutes", group.ID)); err != nil {
		return false, err
	}
	return true, nil
}
