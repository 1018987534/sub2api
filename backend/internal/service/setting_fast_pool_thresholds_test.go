package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fastPoolThresholdSettingsRepo struct {
	openAIAdvancedSchedulerSettingRepoStub
	getErr error
	setErr error
	writes int
}

func (r *fastPoolThresholdSettingsRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.getErr != nil {
		return "", r.getErr
	}
	return r.openAIAdvancedSchedulerSettingRepoStub.GetValue(ctx, key)
}

func (r *fastPoolThresholdSettingsRepo) Set(_ context.Context, key, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	r.writes++
	return nil
}

func TestFastPoolThresholdSettingsPersistAndApplyAcrossInstances(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolThresholdSettingsRepo{}
	control := NewSettingService(repo, nil)
	gateway := NewSettingService(repo, nil)
	initial, err := gateway.GetFastPoolThresholdSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, &FastPoolThresholdSettings{EnterSeconds: 30, ExitSeconds: 33}, initial)
	for _, pair := range [][2]int{{28, 31}, {32, 35}, {35, 35}} {
		saved, err := control.UpdateFastPoolThresholdSettings(ctx, pair[0], pair[1])
		require.NoError(t, err)
		readback, err := gateway.GetFastPoolThresholdSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, saved, readback)
		policy, err := gateway.GetTotalDurationLatencyPolicy(ctx)
		require.NoError(t, err)
		require.Equal(t, time.Duration(pair[0])*time.Second, policy.FastPoolEnterThreshold)
		require.Equal(t, time.Duration(pair[1])*time.Second, policy.FastPoolExitThreshold)
		require.Equal(t, 16, policy.CircuitBreakCount)
	}
	require.Equal(t, 3, repo.writes, "each threshold pair must use a single storage write")
	reloaded, err := NewSettingService(repo, nil).GetFastPoolThresholdSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 35, reloaded.EnterSeconds)

	cache := &totalDurationCircuitPolicyRecorder{}
	limiter := NewRateLimitService(nil, nil, nil, nil, nil)
	limiter.SetSettingService(gateway)
	limiter.SetFirstTokenLatencyStatsCache(cache)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	durationMS := 34000
	usage := &UsageLog{RequestID: "threshold-sample", RequestType: RequestTypeStream, Stream: true, ActualCost: 0.01, DurationMs: &durationMS}
	limiter.ObserveTotalDurationLatency(ctx, account, usage)
	require.Equal(t, 35*time.Second, cache.policies[0].FastPoolEnterThreshold)
	_, err = control.UpdateFastPoolThresholdSettings(ctx, 28, 31)
	require.NoError(t, err)
	limiter.ObserveTotalDurationLatency(ctx, account, usage)
	require.Equal(t, 28*time.Second, cache.policies[1].FastPoolEnterThreshold)
	require.Equal(t, 31*time.Second, cache.policies[1].FastPoolExitThreshold)
}

func TestFastPoolThresholdSettingsRejectInvalidAndFailedWrites(t *testing.T) {
	ctx := context.Background()
	repo := &fastPoolThresholdSettingsRepo{}
	svc := NewSettingService(repo, nil)
	_, err := svc.UpdateFastPoolThresholdSettings(ctx, 30, 33)
	require.NoError(t, err)
	for _, pair := range [][2]int{{0, 33}, {30, 0}, {34, 33}, {1, 86401}, {-1, 33}} {
		_, err := svc.UpdateFastPoolThresholdSettings(ctx, pair[0], pair[1])
		require.Error(t, err)
	}
	require.Equal(t, 1, repo.writes)
	repo.setErr = errors.New("write failed")
	_, err = svc.UpdateFastPoolThresholdSettings(ctx, 28, 31)
	require.Error(t, err)
	saved, err := svc.GetFastPoolThresholdSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, &FastPoolThresholdSettings{EnterSeconds: 30, ExitSeconds: 33}, saved)
	for _, value := range []string{`{}`, `null`, `{"enter_seconds":28}`, `{"enter_seconds":40,"exit_seconds":33}`, `{"enter_seconds":1.5,"exit_seconds":33}`, `{`} {
		repo.values[SettingKeyFastPoolThresholds] = value
		_, err := svc.GetFastPoolThresholdSettings(ctx)
		require.Error(t, err)
	}
	repo.getErr = errors.New("read failed")
	_, err = svc.GetFastPoolThresholdSettings(ctx)
	require.Error(t, err)
}
