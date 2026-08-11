//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type missingFirstTokenLatencySettingRepo struct {
	*mockSettingRepo
	getValueCalls int
}

func (r *missingFirstTokenLatencySettingRepo) GetValue(_ context.Context, _ string) (string, error) {
	r.getValueCalls++
	return "", ErrSettingNotFound
}

func TestFirstTokenLatencyAutoPauseSettings_RoundTripMultipleRules(t *testing.T) {
	svc := NewSettingService(newMockSettingRepo(), &config.Config{})
	input := &FirstTokenLatencyAutoPauseSettings{
		Enabled: true,
		Rules: []FirstTokenLatencyAutoPauseRule{
			{WindowMinutes: 1, ThresholdSeconds: 12.5, TriggerPercent: 25, PauseMinutes: 3},
			{WindowMinutes: 10, ThresholdSeconds: 6, TriggerPercent: 66.7, PauseMinutes: 15},
		},
	}

	updates, err := svc.buildSystemSettingsUpdates(context.Background(), &SystemSettings{
		FirstTokenLatencyAutoPauseSettings: input,
	})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"enabled": true,
		"rules": [
				{"window_minutes":1,"threshold_seconds":12.5,"trigger_percent":25,"pause_minutes":3},
				{"window_minutes":10,"threshold_seconds":6,"trigger_percent":66.7,"pause_minutes":15}
		]
	}`, updates[SettingKeyFirstTokenLatencyAutoPauseSettings])

	parsed := svc.parseSettings(updates)
	require.Equal(t, input, parsed.FirstTokenLatencyAutoPauseSettings)
}

func TestFirstTokenLatencyAutoPauseSettings_RejectsDuplicateAndInvalidRules(t *testing.T) {
	rule := FirstTokenLatencyAutoPauseRule{WindowMinutes: 5, ThresholdSeconds: 10, TriggerPercent: 50, PauseMinutes: 10}

	_, err := validateFirstTokenLatencyAutoPauseSettings(FirstTokenLatencyAutoPauseSettings{
		Enabled: true,
		Rules:   []FirstTokenLatencyAutoPauseRule{rule, rule},
	})
	require.ErrorContains(t, err, "duplicate rule")

	rule.ThresholdSeconds = 0
	_, err = validateFirstTokenLatencyAutoPauseSettings(FirstTokenLatencyAutoPauseSettings{
		Enabled: true,
		Rules:   []FirstTokenLatencyAutoPauseRule{rule},
	})
	require.ErrorContains(t, err, "threshold_seconds")

	rule.ThresholdSeconds = math.NaN()
	_, err = validateFirstTokenLatencyAutoPauseSettings(FirstTokenLatencyAutoPauseSettings{
		Enabled: true,
		Rules:   []FirstTokenLatencyAutoPauseRule{rule},
	})
	require.ErrorContains(t, err, "threshold_seconds")

	rule.ThresholdSeconds = 10
	rule.TriggerPercent = 0
	_, err = validateFirstTokenLatencyAutoPauseSettings(FirstTokenLatencyAutoPauseSettings{
		Enabled: true,
		Rules:   []FirstTokenLatencyAutoPauseRule{rule},
	})
	require.ErrorContains(t, err, "trigger_percent")
}

func TestFirstTokenLatencyAutoPauseSettings_LegacyCountMigratesConservatively(t *testing.T) {
	settings, err := parseFirstTokenLatencyAutoPauseSettings(`{
		"enabled": true,
		"rules": [
			{"window_minutes":1,"threshold_seconds":120,"trigger_count":2,"pause_minutes":360}
		]
	}`)
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, float64(100), settings.Rules[0].TriggerPercent)

	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"enabled":true,
		"rules":[{"window_minutes":1,"threshold_seconds":120,"trigger_percent":100,"pause_minutes":360}]
	}`, string(encoded))
}

func TestGetFirstTokenLatencyAutoPauseSettings_MissingSettingUsesCachedDefault(t *testing.T) {
	repo := &missingFirstTokenLatencySettingRepo{mockSettingRepo: newMockSettingRepo()}
	svc := NewSettingService(repo, &config.Config{})

	first := svc.GetFirstTokenLatencyAutoPauseSettings(context.Background())
	second := svc.GetFirstTokenLatencyAutoPauseSettings(context.Background())

	require.Equal(t, DefaultFirstTokenLatencyAutoPauseSettings(), first)
	require.Equal(t, first, second)
	require.Equal(t, 1, repo.getValueCalls)
}

func TestGetFirstTokenLatencyAutoPauseSettings_DefaultDisabled(t *testing.T) {
	svc := NewSettingService(newMockSettingRepo(), &config.Config{})
	settings := svc.GetFirstTokenLatencyAutoPauseSettings(context.Background())

	require.False(t, settings.Enabled)
	require.Equal(t, DefaultFirstTokenLatencyAutoPauseSettings(), settings)
}
