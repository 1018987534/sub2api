package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SettingKeyFastPoolPriorityMode                      = "fast_pool_priority_mode"
	SettingKeyFastPoolPriorityBelowThresholdMode        = "fast_pool_priority_below_threshold_mode"
	SettingKeyFastPoolPriorityCacheRateThresholdPercent = "fast_pool_priority_cache_rate_threshold_percent"
)

type FastPoolPriorityMode string

const (
	FastPoolPriorityIntelligence FastPoolPriorityMode = "intelligence"
	FastPoolPriorityLatency      FastPoolPriorityMode = "latency"
	FastPoolPriorityRate         FastPoolPriorityMode = "rate"
)

type FastPoolPrioritySettings struct {
	Mode                      FastPoolPriorityMode `json:"mode"`
	BelowThresholdMode        FastPoolPriorityMode `json:"below_threshold_mode"`
	CacheRateThresholdPercent *float64             `json:"cache_rate_threshold_percent"`
}

func validFastPoolPriorityMode(mode FastPoolPriorityMode) bool {
	return mode == FastPoolPriorityIntelligence || mode == FastPoolPriorityLatency || mode == FastPoolPriorityRate
}

func validFastPoolCacheRateThreshold(percent float64) bool {
	return !math.IsNaN(percent) && !math.IsInf(percent, 0) && percent >= 0 && percent <= 100
}

func defaultFastPoolPrioritySettings() *FastPoolPrioritySettings {
	threshold := 80.0
	return &FastPoolPrioritySettings{Mode: FastPoolPriorityIntelligence, BelowThresholdMode: FastPoolPriorityIntelligence, CacheRateThresholdPercent: &threshold}
}

// Read shared storage for each new ordering decision so all running gateways
// see a saved change without a process-local settings cache or restart.
func (s *SettingService) GetFastPoolPrioritySettings(ctx context.Context) (*FastPoolPrioritySettings, error) {
	settings := defaultFastPoolPrioritySettings()
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	values, err := s.settingRepo.GetMultiple(dbCtx, []string{SettingKeyFastPoolPriorityMode, SettingKeyFastPoolPriorityBelowThresholdMode, SettingKeyFastPoolPriorityCacheRateThresholdPercent})
	if err != nil {
		return nil, fmt.Errorf("get fast-pool priority settings: %w", err)
	}
	if mode := FastPoolPriorityMode(strings.TrimSpace(values[SettingKeyFastPoolPriorityMode])); mode != "" {
		if !validFastPoolPriorityMode(mode) {
			return nil, fmt.Errorf("invalid stored fast-pool priority mode")
		}
		settings.Mode = mode
	}
	if mode := FastPoolPriorityMode(strings.TrimSpace(values[SettingKeyFastPoolPriorityBelowThresholdMode])); mode != "" {
		if !validFastPoolPriorityMode(mode) {
			return nil, fmt.Errorf("invalid stored fast-pool below-threshold priority mode")
		}
		settings.BelowThresholdMode = mode
	}
	if value, configured := values[SettingKeyFastPoolPriorityCacheRateThresholdPercent]; configured {
		settings.CacheRateThresholdPercent = nil
		if value = strings.TrimSpace(value); value != "" {
			percent, err := strconv.ParseFloat(value, 64)
			if err != nil || !validFastPoolCacheRateThreshold(percent) {
				return nil, fmt.Errorf("invalid stored fast-pool cache-rate threshold")
			}
			settings.CacheRateThresholdPercent = &percent
		}
	}
	return settings, nil
}

func (s *SettingService) UpdateFastPoolPrioritySettings(ctx context.Context, mode FastPoolPriorityMode, cacheRateThresholdPercent *float64, belowThresholdMode FastPoolPriorityMode) (*FastPoolPrioritySettings, error) {
	if !validFastPoolPriorityMode(mode) {
		return nil, infraerrors.BadRequest("FAST_POOL_PRIORITY_MODE_INVALID", "mode must be intelligence, latency, or rate")
	}
	if !validFastPoolPriorityMode(belowThresholdMode) {
		return nil, infraerrors.BadRequest("FAST_POOL_BELOW_THRESHOLD_MODE_INVALID", "below_threshold_mode must be intelligence, latency, or rate")
	}
	if cacheRateThresholdPercent != nil && !validFastPoolCacheRateThreshold(*cacheRateThresholdPercent) {
		return nil, infraerrors.BadRequest("FAST_POOL_CACHE_RATE_THRESHOLD_INVALID", "cache_rate_threshold_percent must be null or a percentage from 0 to 100")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("fast-pool priority settings storage is unavailable")
	}
	threshold := ""
	if cacheRateThresholdPercent != nil {
		threshold = strconv.FormatFloat(*cacheRateThresholdPercent, 'f', -1, 64)
	}
	// Save both strategies and the optional threshold as one shared snapshot.
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyFastPoolPriorityMode:                      string(mode),
		SettingKeyFastPoolPriorityBelowThresholdMode:        string(belowThresholdMode),
		SettingKeyFastPoolPriorityCacheRateThresholdPercent: threshold,
	}); err != nil {
		return nil, fmt.Errorf("save fast-pool priority settings: %w", err)
	}
	return &FastPoolPrioritySettings{Mode: mode, BelowThresholdMode: belowThresholdMode, CacheRateThresholdPercent: cacheRateThresholdPercent}, nil
}

func (s *RateLimitService) fastPoolPrioritySettings(ctx context.Context) *FastPoolPrioritySettings {
	if s == nil || s.settingService == nil {
		return defaultFastPoolPrioritySettings()
	}
	settings, err := s.settingService.GetFastPoolPrioritySettings(ctx)
	if err != nil {
		slog.Warn("read fast-pool priority setting failed; retaining cache-rate priority", "error", err)
		return defaultFastPoolPrioritySettings()
	}
	return settings
}
