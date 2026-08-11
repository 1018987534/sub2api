package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	firstTokenLatencyAutoPauseCacheTTL  = 60 * time.Second
	firstTokenLatencyAutoPauseErrorTTL  = 5 * time.Second
	firstTokenLatencyAutoPauseDBTimeout = 5 * time.Second
	maxFirstTokenLatencyAutoPauseRules  = 20
)

type FirstTokenLatencyAutoPauseRule struct {
	WindowMinutes    int     `json:"window_minutes"`
	ThresholdSeconds float64 `json:"threshold_seconds"`
	TriggerCount     int     `json:"trigger_count"`
	PauseMinutes     int     `json:"pause_minutes"`
}

type FirstTokenLatencyAutoPauseSettings struct {
	Enabled bool                             `json:"enabled"`
	Rules   []FirstTokenLatencyAutoPauseRule `json:"rules"`
}

func firstTokenLatencyRuleKey(rule FirstTokenLatencyAutoPauseRule) string {
	raw := fmt.Sprintf("%d|%.3f|%d|%d", rule.WindowMinutes, rule.ThresholdSeconds, rule.TriggerCount, rule.PauseMinutes)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:12])
}

type cachedFirstTokenLatencyAutoPauseSettings struct {
	settings  FirstTokenLatencyAutoPauseSettings
	expiresAt int64
}

func DefaultFirstTokenLatencyAutoPauseSettings() FirstTokenLatencyAutoPauseSettings {
	return FirstTokenLatencyAutoPauseSettings{
		Enabled: false,
		Rules: []FirstTokenLatencyAutoPauseRule{
			{WindowMinutes: 5, ThresholdSeconds: 10, TriggerCount: 1, PauseMinutes: 10},
		},
	}
}

func cloneFirstTokenLatencyAutoPauseSettings(input FirstTokenLatencyAutoPauseSettings) FirstTokenLatencyAutoPauseSettings {
	cloned := input
	cloned.Rules = append([]FirstTokenLatencyAutoPauseRule(nil), input.Rules...)
	return cloned
}

func validateFirstTokenLatencyAutoPauseSettings(input FirstTokenLatencyAutoPauseSettings) (FirstTokenLatencyAutoPauseSettings, error) {
	if len(input.Rules) == 0 {
		return FirstTokenLatencyAutoPauseSettings{}, infraerrors.BadRequest(
			"INVALID_FIRST_TOKEN_LATENCY_AUTO_PAUSE_SETTINGS",
			"at least one first-token latency auto-pause rule is required",
		)
	}
	if len(input.Rules) > maxFirstTokenLatencyAutoPauseRules {
		return FirstTokenLatencyAutoPauseSettings{}, infraerrors.BadRequest(
			"INVALID_FIRST_TOKEN_LATENCY_AUTO_PAUSE_SETTINGS",
			fmt.Sprintf("first-token latency auto-pause rules cannot exceed %d", maxFirstTokenLatencyAutoPauseRules),
		)
	}

	normalized := FirstTokenLatencyAutoPauseSettings{
		Enabled: input.Enabled,
		Rules:   make([]FirstTokenLatencyAutoPauseRule, 0, len(input.Rules)),
	}
	seen := make(map[string]struct{}, len(input.Rules))
	for index, rule := range input.Rules {
		if rule.WindowMinutes < 1 || rule.WindowMinutes > 1440 {
			return FirstTokenLatencyAutoPauseSettings{}, invalidFirstTokenLatencyRule(index, "window_minutes must be between 1 and 1440")
		}
		if math.IsNaN(rule.ThresholdSeconds) || math.IsInf(rule.ThresholdSeconds, 0) || rule.ThresholdSeconds < 0.1 || rule.ThresholdSeconds > 600 {
			return FirstTokenLatencyAutoPauseSettings{}, invalidFirstTokenLatencyRule(index, "threshold_seconds must be between 0.1 and 600")
		}
		if rule.TriggerCount < 1 || rule.TriggerCount > 100 {
			return FirstTokenLatencyAutoPauseSettings{}, invalidFirstTokenLatencyRule(index, "trigger_count must be between 1 and 100")
		}
		if rule.PauseMinutes < 1 || rule.PauseMinutes > 1440 {
			return FirstTokenLatencyAutoPauseSettings{}, invalidFirstTokenLatencyRule(index, "pause_minutes must be between 1 and 1440")
		}

		rule.ThresholdSeconds = float64(int(rule.ThresholdSeconds*1000+0.5)) / 1000
		key := firstTokenLatencyRuleKey(rule)
		if _, exists := seen[key]; exists {
			return FirstTokenLatencyAutoPauseSettings{}, invalidFirstTokenLatencyRule(index, "duplicate rule")
		}
		seen[key] = struct{}{}
		normalized.Rules = append(normalized.Rules, rule)
	}
	return normalized, nil
}

func invalidFirstTokenLatencyRule(index int, message string) error {
	return infraerrors.BadRequest(
		"INVALID_FIRST_TOKEN_LATENCY_AUTO_PAUSE_SETTINGS",
		fmt.Sprintf("first-token latency auto-pause rule %d: %s", index+1, message),
	)
}

func parseFirstTokenLatencyAutoPauseSettings(raw string) (FirstTokenLatencyAutoPauseSettings, error) {
	defaults := DefaultFirstTokenLatencyAutoPauseSettings()
	if strings.TrimSpace(raw) == "" {
		return defaults, nil
	}
	var parsed FirstTokenLatencyAutoPauseSettings
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return defaults, err
	}
	normalized, err := validateFirstTokenLatencyAutoPauseSettings(parsed)
	if err != nil {
		return defaults, err
	}
	return normalized, nil
}

func (s *SettingService) GetFirstTokenLatencyAutoPauseSettings(ctx context.Context) FirstTokenLatencyAutoPauseSettings {
	defaults := DefaultFirstTokenLatencyAutoPauseSettings()
	if s == nil || s.settingRepo == nil {
		return defaults
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cached, _ := s.firstTokenLatencyAutoPauseCache.Load().(*cachedFirstTokenLatencyAutoPauseSettings); cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cloneFirstTokenLatencyAutoPauseSettings(cached.settings)
	}

	result, err, _ := s.firstTokenLatencyAutoPauseSF.Do(SettingKeyFirstTokenLatencyAutoPauseSettings, func() (any, error) {
		if cached, _ := s.firstTokenLatencyAutoPauseCache.Load().(*cachedFirstTokenLatencyAutoPauseSettings); cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cloneFirstTokenLatencyAutoPauseSettings(cached.settings), nil
		}

		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), firstTokenLatencyAutoPauseDBTimeout)
		defer cancel()
		raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyFirstTokenLatencyAutoPauseSettings)
		if err != nil {
			if errors.Is(err, ErrSettingNotFound) {
				s.firstTokenLatencyAutoPauseCache.Store(&cachedFirstTokenLatencyAutoPauseSettings{
					settings:  defaults,
					expiresAt: time.Now().Add(firstTokenLatencyAutoPauseCacheTTL).UnixNano(),
				})
				return defaults, nil
			}
			slog.Warn("failed to get first-token latency auto-pause settings, disabling temporarily", "error", err)
			s.firstTokenLatencyAutoPauseCache.Store(&cachedFirstTokenLatencyAutoPauseSettings{
				settings:  defaults,
				expiresAt: time.Now().Add(firstTokenLatencyAutoPauseErrorTTL).UnixNano(),
			})
			return defaults, nil
		}

		settings, parseErr := parseFirstTokenLatencyAutoPauseSettings(raw)
		if parseErr != nil {
			slog.Warn("failed to parse first-token latency auto-pause settings, disabling", "error", parseErr)
			settings = defaults
		}
		s.firstTokenLatencyAutoPauseCache.Store(&cachedFirstTokenLatencyAutoPauseSettings{
			settings:  cloneFirstTokenLatencyAutoPauseSettings(settings),
			expiresAt: time.Now().Add(firstTokenLatencyAutoPauseCacheTTL).UnixNano(),
		})
		return settings, nil
	})
	if err != nil {
		return defaults
	}
	settings, ok := result.(FirstTokenLatencyAutoPauseSettings)
	if !ok {
		return defaults
	}
	return cloneFirstTokenLatencyAutoPauseSettings(settings)
}
