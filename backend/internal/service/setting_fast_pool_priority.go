package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyFastPoolPriorityMode = "fast_pool_priority_mode"

type FastPoolPriorityMode string

const (
	FastPoolPriorityIntelligence FastPoolPriorityMode = "intelligence"
	FastPoolPriorityRate         FastPoolPriorityMode = "rate"
)

type FastPoolPrioritySettings struct {
	Mode FastPoolPriorityMode `json:"mode"`
}

func validFastPoolPriorityMode(mode FastPoolPriorityMode) bool {
	return mode == FastPoolPriorityIntelligence || mode == FastPoolPriorityRate
}

// Read shared storage for each new ordering decision so all running gateways
// see a saved change without a process-local settings cache or restart.
func (s *SettingService) GetFastPoolPrioritySettings(ctx context.Context) (*FastPoolPrioritySettings, error) {
	settings := &FastPoolPrioritySettings{Mode: FastPoolPriorityIntelligence}
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	value, err := s.settingRepo.GetValue(dbCtx, SettingKeyFastPoolPriorityMode)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(value) == "") {
		return settings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get fast-pool priority mode: %w", err)
	}
	mode := FastPoolPriorityMode(strings.TrimSpace(value))
	if !validFastPoolPriorityMode(mode) {
		return nil, fmt.Errorf("invalid stored fast-pool priority mode")
	}
	settings.Mode = mode
	return settings, nil
}

func (s *SettingService) UpdateFastPoolPrioritySettings(ctx context.Context, mode FastPoolPriorityMode) (*FastPoolPrioritySettings, error) {
	if !validFastPoolPriorityMode(mode) {
		return nil, infraerrors.BadRequest("FAST_POOL_PRIORITY_MODE_INVALID", "mode must be intelligence or rate")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("fast-pool priority settings storage is unavailable")
	}
	if err := s.settingRepo.Set(ctx, SettingKeyFastPoolPriorityMode, string(mode)); err != nil {
		return nil, fmt.Errorf("save fast-pool priority mode: %w", err)
	}
	return &FastPoolPrioritySettings{Mode: mode}, nil
}

func (s *RateLimitService) fastPoolPriorityMode(ctx context.Context) FastPoolPriorityMode {
	if s == nil || s.settingService == nil {
		return FastPoolPriorityIntelligence
	}
	settings, err := s.settingService.GetFastPoolPrioritySettings(ctx)
	if err != nil {
		slog.Warn("read fast-pool priority setting failed; retaining cache-rate priority", "error", err)
		return FastPoolPriorityIntelligence
	}
	return settings.Mode
}
