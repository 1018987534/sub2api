package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Store the pair in one setting so concurrent readers never combine different revisions.
const SettingKeyFastPoolThresholds = "fast_pool_thresholds"

type FastPoolThresholdSettings struct {
	EnterSeconds int `json:"enter_seconds"`
	ExitSeconds  int `json:"exit_seconds"`
}

func validFastPoolThresholds(settings FastPoolThresholdSettings) bool {
	return settings.EnterSeconds >= 1 && settings.ExitSeconds >= settings.EnterSeconds && settings.ExitSeconds <= 86400
}

func (s *SettingService) GetFastPoolThresholdSettings(ctx context.Context) (*FastPoolThresholdSettings, error) {
	defaults := DefaultTotalDurationLatencyPolicy()
	settings := &FastPoolThresholdSettings{
		EnterSeconds: int(defaults.FastPoolEnterThreshold / time.Second),
		ExitSeconds:  int(defaults.FastPoolExitThreshold / time.Second),
	}
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	value, err := s.settingRepo.GetValue(dbCtx, SettingKeyFastPoolThresholds)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(value) == "") {
		return settings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get fast-pool thresholds: %w", err)
	}
	var stored FastPoolThresholdSettings
	if err := json.Unmarshal([]byte(value), &stored); err != nil || !validFastPoolThresholds(stored) {
		return nil, fmt.Errorf("invalid stored fast-pool thresholds")
	}
	return &stored, nil
}

func (s *SettingService) UpdateFastPoolThresholdSettings(ctx context.Context, enterSeconds, exitSeconds int) (*FastPoolThresholdSettings, error) {
	settings := &FastPoolThresholdSettings{EnterSeconds: enterSeconds, ExitSeconds: exitSeconds}
	if !validFastPoolThresholds(*settings) {
		return nil, infraerrors.BadRequest("FAST_POOL_THRESHOLDS_INVALID", "thresholds must be whole seconds from 1 to 86400, with exit_seconds >= enter_seconds")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("fast-pool thresholds storage is unavailable")
	}
	value, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyFastPoolThresholds, string(value)); err != nil {
		return nil, fmt.Errorf("save fast-pool thresholds: %w", err)
	}
	return settings, nil
}
