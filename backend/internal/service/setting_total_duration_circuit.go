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

const (
	SettingKeyTotalDurationCircuitBreakCount            = "total_duration_circuit_break_count"
	SettingKeyTotalDurationCircuitBreakThresholdSeconds = "total_duration_circuit_break_threshold_seconds"
	SettingKeyTotalDurationCircuitBreakWindowSeconds    = "total_duration_circuit_break_window_seconds"

	totalDurationCircuitBreakCountMin = 1
	totalDurationCircuitBreakCountMax = 10000
	// The dashboard edits these values in whole minutes, while the shared
	// setting and scheduler policy retain seconds for compatibility.
	totalDurationCircuitBreakThresholdSecondsMin = 1
	totalDurationCircuitBreakThresholdSecondsMax = 24 * 60 * 60
	totalDurationCircuitBreakWindowSecondsMin    = 1
	totalDurationCircuitBreakWindowSecondsMax    = 24 * 60 * 60
)

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
	stored, found, err := readTotalDurationCircuitSetting(dbCtx, s.settingRepo, SettingKeyTotalDurationCircuitBreakCount, totalDurationCircuitBreakCountMin, totalDurationCircuitBreakCountMax)
	if err != nil {
		return policy, fmt.Errorf("get total-duration circuit breaker count: %w", err)
	}
	if found {
		policy.CircuitBreakCount = stored
	}
	stored, found, err = readTotalDurationCircuitSetting(dbCtx, s.settingRepo, SettingKeyTotalDurationCircuitBreakThresholdSeconds, totalDurationCircuitBreakThresholdSecondsMin, totalDurationCircuitBreakThresholdSecondsMax)
	if err != nil {
		return policy, fmt.Errorf("get total-duration circuit breaker threshold: %w", err)
	}
	if found {
		policy.CircuitBreakThreshold = time.Duration(stored) * time.Second
	}
	stored, found, err = readTotalDurationCircuitSetting(dbCtx, s.settingRepo, SettingKeyTotalDurationCircuitBreakWindowSeconds, totalDurationCircuitBreakWindowSecondsMin, totalDurationCircuitBreakWindowSecondsMax)
	if err != nil {
		return policy, fmt.Errorf("get total-duration circuit breaker window: %w", err)
	}
	if found {
		policy.CircuitBreakWindow = time.Duration(stored) * time.Second
	}
	thresholds, err := s.GetFastPoolThresholdSettings(dbCtx)
	if err != nil {
		return policy, err
	}
	policy.FastPoolEnterThreshold = time.Duration(thresholds.EnterSeconds) * time.Second
	policy.FastPoolExitThreshold = time.Duration(thresholds.ExitSeconds) * time.Second
	return policy, nil
}

func readTotalDurationCircuitSetting(ctx context.Context, repo SettingRepository, key string, min, max int) (int, bool, error) {
	value, err := repo.GetValue(ctx, key)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(value) == "") {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < min || parsed > max {
		return 0, false, fmt.Errorf("invalid stored setting %s", key)
	}
	return parsed, true, nil
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

func (s *SettingService) UpdateTotalDurationCircuitBreakerSettings(ctx context.Context, maxSlowRequests, thresholdSeconds, windowSeconds int) (*TotalDurationCircuitBreakerSettings, error) {
	if maxSlowRequests < 0 || maxSlowRequests >= totalDurationCircuitBreakCountMax {
		return nil, infraerrors.BadRequest("TOTAL_DURATION_CIRCUIT_LIMIT_INVALID", "max_slow_requests must be an integer between 0 and 9999")
	}
	if thresholdSeconds < totalDurationCircuitBreakThresholdSecondsMin || thresholdSeconds > totalDurationCircuitBreakThresholdSecondsMax {
		return nil, infraerrors.BadRequest("TOTAL_DURATION_CIRCUIT_THRESHOLD_INVALID", "threshold_seconds must be an integer between 1 and 86400")
	}
	if windowSeconds < totalDurationCircuitBreakWindowSecondsMin || windowSeconds > totalDurationCircuitBreakWindowSecondsMax {
		return nil, infraerrors.BadRequest("TOTAL_DURATION_CIRCUIT_WINDOW_INVALID", "window_seconds must be an integer between 1 and 86400")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("total-duration circuit breaker settings storage is unavailable")
	}
	policy := s.defaultTotalDurationLatencyPolicy()
	policy.CircuitBreakCount = maxSlowRequests + 1
	policy.CircuitBreakThreshold = time.Duration(thresholdSeconds) * time.Second
	policy.CircuitBreakWindow = time.Duration(windowSeconds) * time.Second
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyTotalDurationCircuitBreakCount:            strconv.Itoa(policy.CircuitBreakCount),
		SettingKeyTotalDurationCircuitBreakThresholdSeconds: strconv.Itoa(thresholdSeconds),
		SettingKeyTotalDurationCircuitBreakWindowSeconds:    strconv.Itoa(windowSeconds),
	}); err != nil {
		return nil, fmt.Errorf("save total-duration circuit breaker settings: %w", err)
	}
	return totalDurationCircuitBreakerSettings(policy), nil
}
