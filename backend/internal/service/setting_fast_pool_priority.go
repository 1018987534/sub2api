package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SettingKeyFastPoolPriorityMode                      = "fast_pool_priority_mode"
	SettingKeyFastPoolPriorityBelowThresholdMode        = "fast_pool_priority_below_threshold_mode"
	SettingKeyFastPoolPriorityCacheRateThresholdPercent = "fast_pool_priority_cache_rate_threshold_percent"
	SettingKeyFastPoolCostEffectivenessGroupIDs         = "fast_pool_cost_effectiveness_group_ids"
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
	CostEffectivenessGroupIDs []int64              `json:"cost_effectiveness_group_ids"`
}

func (s *FastPoolPrioritySettings) CostEffectivenessEnabled(groupID int64) bool {
	if s == nil || groupID <= 0 {
		return false
	}
	for _, id := range s.CostEffectivenessGroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

func validFastPoolPriorityMode(mode FastPoolPriorityMode) bool {
	return mode == FastPoolPriorityIntelligence || mode == FastPoolPriorityLatency || mode == FastPoolPriorityRate
}

func validFastPoolCacheRateThreshold(percent float64) bool {
	return !math.IsNaN(percent) && !math.IsInf(percent, 0) && percent >= 0 && percent <= 100
}

func defaultFastPoolPrioritySettings() *FastPoolPrioritySettings {
	threshold := 80.0
	return &FastPoolPrioritySettings{Mode: FastPoolPriorityIntelligence, BelowThresholdMode: FastPoolPriorityIntelligence, CacheRateThresholdPercent: &threshold, CostEffectivenessGroupIDs: []int64{}}
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
	values, err := s.settingRepo.GetMultiple(dbCtx, []string{SettingKeyFastPoolPriorityMode, SettingKeyFastPoolPriorityBelowThresholdMode, SettingKeyFastPoolPriorityCacheRateThresholdPercent, SettingKeyFastPoolCostEffectivenessGroupIDs})
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
	if value := strings.TrimSpace(values[SettingKeyFastPoolCostEffectivenessGroupIDs]); value != "" {
		if err := json.Unmarshal([]byte(value), &settings.CostEffectivenessGroupIDs); err != nil {
			return nil, fmt.Errorf("invalid stored cost-effectiveness group IDs: %w", err)
		}
		for _, id := range settings.CostEffectivenessGroupIDs {
			if id <= 0 {
				return nil, fmt.Errorf("invalid stored cost-effectiveness group ID")
			}
		}
	}
	return settings, nil
}

// A group override changes only its own membership in the opt-in list. Global
// cache tiers and strategies remain untouched, including saves from old clients.
func (s *SettingService) UpdateFastPoolCostEffectivenessGroup(ctx context.Context, groupID int64, enabled bool) (*FastPoolPrioritySettings, error) {
	if groupID <= 0 {
		return nil, infraerrors.BadRequest("FAST_POOL_GROUP_INVALID", "group_id must be positive")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("fast-pool priority settings storage is unavailable")
	}
	settings, err := s.GetFastPoolPrioritySettings(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(settings.CostEffectivenessGroupIDs)+1)
	seen := map[int64]bool{}
	for _, id := range settings.CostEffectivenessGroupIDs {
		if id != groupID && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if enabled {
		ids = append(ids, groupID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{SettingKeyFastPoolCostEffectivenessGroupIDs: string(raw)}); err != nil {
		return nil, fmt.Errorf("save cost-effectiveness group override: %w", err)
	}
	settings.CostEffectivenessGroupIDs = ids
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
	return s.GetFastPoolPrioritySettings(ctx)
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
