package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

const FirstTokenLatencyAutoPauseReasonSource = "first_token_latency_auto_pause"

type firstTokenLatencyRuleMatch struct {
	index           int
	rule            FirstTokenLatencyAutoPauseRule
	totalCount      int64
	slowCount       int64
	observedPercent float64
	thresholdMS     int
}

// ObserveFirstTokenLatency records one successful request's first-token latency.
// Rules are ORed; when several rules fire on the same event, the longest pause wins.
func (s *RateLimitService) ObserveFirstTokenLatency(ctx context.Context, account *Account, requestID string, firstTokenMs *int) bool {
	if s == nil || s.settingService == nil || s.firstTokenLatencyCounterCache == nil || s.accountRepo == nil || account == nil || firstTokenMs == nil {
		return false
	}
	if account.ID <= 0 || *firstTokenMs < 0 || !account.IsActive() || !account.Schedulable || !account.IsSchedulable() {
		return false
	}

	settings := s.settingService.GetFirstTokenLatencyAutoPauseSettings(ctx)
	if !settings.Enabled || len(settings.Rules) == 0 {
		return false
	}

	eventID := strings.TrimSpace(requestID)
	if eventID == "" {
		eventID = uuid.NewString()
	}
	var winner *firstTokenLatencyRuleMatch
	for index, rule := range settings.Rules {
		thresholdMS := int(math.Round(rule.ThresholdSeconds * 1000))
		isSlow := *firstTokenMs > thresholdMS
		counts, err := s.firstTokenLatencyCounterCache.RecordFirstTokenSample(
			ctx,
			account.ID,
			firstTokenLatencyRuleKey(rule),
			rule.WindowMinutes*60,
			eventID,
			isSlow,
		)
		if err != nil {
			slog.Warn("first_token_latency_sample_record_failed", "account_id", account.ID, "rule_index", index, "error", err)
			continue
		}
		if !isSlow || counts.Total <= 0 || counts.Slow < 0 {
			continue
		}
		observedPercent := float64(counts.Slow) * 100 / float64(counts.Total)
		if observedPercent+1e-9 < rule.TriggerPercent {
			continue
		}
		matched := &firstTokenLatencyRuleMatch{
			index:           index,
			rule:            rule,
			totalCount:      counts.Total,
			slowCount:       counts.Slow,
			observedPercent: observedPercent,
			thresholdMS:     thresholdMS,
		}
		if winner == nil || matched.rule.PauseMinutes > winner.rule.PauseMinutes {
			winner = matched
		}
	}
	if winner == nil {
		return false
	}

	claimed, err := s.firstTokenLatencyCounterCache.ClaimFirstTokenPause(ctx, account.ID, winner.rule.PauseMinutes*60)
	if err != nil {
		slog.Warn("first_token_latency_pause_claim_failed", "account_id", account.ID, "error", err)
		return false
	}
	if !claimed {
		return false
	}

	now := time.Now().UTC()
	until := now.Add(time.Duration(winner.rule.PauseMinutes) * time.Minute)
	state := &TempUnschedState{
		Source:                FirstTokenLatencyAutoPauseReasonSource,
		UntilUnix:             until.Unix(),
		TriggeredAtUnix:       now.Unix(),
		RuleIndex:             winner.index,
		ErrorMessage:          fmt.Sprintf("first-token latency %dms exceeded %dms; slow-request share %.3f%% (%d/%d) reached %.3f%% within %d minutes; scheduling paused for %d minutes", *firstTokenMs, winner.thresholdMS, winner.observedPercent, winner.slowCount, winner.totalCount, winner.rule.TriggerPercent, winner.rule.WindowMinutes, winner.rule.PauseMinutes),
		TriggerWindowMinutes:  winner.rule.WindowMinutes,
		TriggerMode:           "first_token_latency",
		FirstTokenMs:          *firstTokenMs,
		FirstTokenThresholdMs: winner.thresholdMS,
		SampleCount:           winner.totalCount,
		SlowSampleCount:       winner.slowCount,
		ObservedPercent:       winner.observedPercent,
		TriggerPercent:        winner.rule.TriggerPercent,
		PauseMinutes:          winner.rule.PauseMinutes,
	}
	reasonBytes, marshalErr := json.Marshal(state)
	if marshalErr != nil {
		_ = s.firstTokenLatencyCounterCache.ReleaseFirstTokenPauseClaim(ctx, account.ID)
		slog.Warn("first_token_latency_pause_reason_marshal_failed", "account_id", account.ID, "error", marshalErr)
		return false
	}
	reason := string(reasonBytes)

	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		_ = s.firstTokenLatencyCounterCache.ReleaseFirstTokenPauseClaim(ctx, account.ID)
		slog.Warn("first_token_latency_set_temp_unsched_failed", "account_id", account.ID, "until", until, "error", err)
		return false
	}

	account.TempUnschedulableUntil = &until
	account.TempUnschedulableReason = reason
	s.notifyAccountSchedulingBlocked(account, until, FirstTokenLatencyAutoPauseReasonSource)
	if s.tempUnschedCache != nil {
		if err := s.tempUnschedCache.SetTempUnsched(ctx, account.ID, state); err != nil {
			slog.Warn("first_token_latency_temp_unsched_cache_set_failed", "account_id", account.ID, "error", err)
		}
	}
	if err := s.firstTokenLatencyCounterCache.ResetFirstTokenSamples(ctx, account.ID); err != nil {
		slog.Warn("first_token_latency_counter_reset_failed", "account_id", account.ID, "error", err)
	}

	slog.Info("first_token_latency_temp_unschedulable",
		"account_id", account.ID,
		"rule_index", winner.index,
		"first_token_ms", *firstTokenMs,
		"threshold_ms", winner.thresholdMS,
		"sample_count", winner.totalCount,
		"slow_sample_count", winner.slowCount,
		"observed_percent", winner.observedPercent,
		"trigger_percent", winner.rule.TriggerPercent,
		"window_minutes", winner.rule.WindowMinutes,
		"pause_minutes", winner.rule.PauseMinutes,
		"until", until)
	return true
}
