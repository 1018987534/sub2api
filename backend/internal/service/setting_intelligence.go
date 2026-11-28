package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/intelligence"
)

const settingKeyIntelligencePauseThreshold = "intelligence_pause_threshold"

func (s *SettingService) GetIntelligencePauseThreshold(ctx context.Context) (int, error) {
	if s == nil || s.settingRepo == nil {
		return 0, errors.New("intelligence settings repository unavailable")
	}
	value, err := s.settingRepo.GetValue(ctx, settingKeyIntelligencePauseThreshold)
	if errors.Is(err, ErrSettingNotFound) {
		return intelligence.DefaultPauseThreshold, nil
	}
	if err != nil {
		return 0, err
	}
	threshold, err := strconv.Atoi(value)
	if err != nil || !intelligence.ValidPauseThreshold(threshold) {
		return 0, intelligence.ErrInvalidPauseThreshold
	}
	return threshold, nil
}

func (s *SettingService) SetIntelligencePauseThreshold(ctx context.Context, threshold int) error {
	if !intelligence.ValidPauseThreshold(threshold) {
		return intelligence.ErrInvalidPauseThreshold
	}
	if s == nil || s.settingRepo == nil {
		return errors.New("intelligence settings repository unavailable")
	}
	return s.settingRepo.Set(ctx, settingKeyIntelligencePauseThreshold, strconv.Itoa(threshold))
}
