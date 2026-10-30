package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyTotalDurationCircuitBreakCount = "total_duration_circuit_break_count"

type TotalDurationCircuitBreakerSettings struct {
	MaxSlowRequests  int `json:"max_slow_requests"`
	ThresholdSeconds int `json:"threshold_seconds"`
	WindowSeconds    int `json:"window_seconds"`
}

func (s *SettingService) defaultTotalDurationLatencyPolicy() TotalDurationLatencyPolicy {
	policy := DefaultTotalDurationLatencyPolicy()
	if s != nil && s.cfg != nil {
		policy = NormalizeTotalDurationLatencyPolicy(TotalDurationLatencyPolicy{
			CircuitBreakThreshold: time.Duration(s.cfg.Gateway.TotalDurationCircuitBreakThresholdSeconds) * time.Second,
			CircuitBreakCount:     s.cfg.Gateway.TotalDurationCircuitBreakCount,
			CircuitBreakWindow:    time.Duration(s.cfg.Gateway.TotalDurationCircuitBreakWindowSeconds) * time.Second,
		})
	}
	return policy
}

// Read shared storage for each completed billable stream, rather than caching
// a node-local count or querying settings during candidate-account iteration.
// A saved limit therefore applies to the next completion on every gateway.
func (s *SettingService) GetTotalDurationLatencyPolicy(ctx context.Context) (TotalDurationLatencyPolicy, error) {
	policy := s.defaultTotalDurationLatencyPolicy()
	if s == nil || s.settingRepo == nil {
		return policy, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	value, err := s.settingRepo.GetValue(dbCtx, SettingKeyTotalDurationCircuitBreakCount)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(value) == "") {
		return policy, nil
	}
	if err != nil {
		return policy, fmt.Errorf("get total-duration circuit breaker count: %w", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || count < 1 || count > 10000 {
		return policy, fmt.Errorf("invalid stored total-duration circuit breaker count")
	}
	policy.CircuitBreakCount = count
	return policy, nil
}

func totalDurationCircuitBreakerSettings(policy TotalDurationLatencyPolicy) *TotalDurationCircuitBreakerSettings {
	return &TotalDurationCircuitBreakerSettings{
		MaxSlowRequests:  policy.CircuitBreakCount - 1,
		ThresholdSeconds: int(policy.CircuitBreakThreshold / time.Second),
		WindowSeconds:    int(policy.CircuitBreakWindow / time.Second),
	}
}

func (s *SettingService) GetTotalDurationCircuitBreakerSettings(ctx context.Context) (*TotalDurationCircuitBreakerSettings, error) {
	policy, err := s.GetTotalDurationLatencyPolicy(ctx)
	if err != nil {
		return nil, err
	}
	return totalDurationCircuitBreakerSettings(policy), nil
}

func (s *SettingService) UpdateTotalDurationCircuitBreakerSettings(ctx context.Context, maxSlowRequests int) (*TotalDurationCircuitBreakerSettings, error) {
	if maxSlowRequests < 0 || maxSlowRequests > 9999 {
		return nil, infraerrors.BadRequest("TOTAL_DURATION_CIRCUIT_LIMIT_INVALID", "max_slow_requests must be an integer between 0 and 9999")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("total-duration circuit breaker settings storage is unavailable")
	}
	policy := s.defaultTotalDurationLatencyPolicy()
	policy.CircuitBreakCount = maxSlowRequests + 1
	if err := s.settingRepo.Set(ctx, SettingKeyTotalDurationCircuitBreakCount, strconv.Itoa(policy.CircuitBreakCount)); err != nil {
		return nil, fmt.Errorf("save total-duration circuit breaker count: %w", err)
	}
	return totalDurationCircuitBreakerSettings(policy), nil
}
