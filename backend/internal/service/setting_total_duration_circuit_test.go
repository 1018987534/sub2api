package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type totalDurationCircuitSettingsRepo struct {
	SettingRepository
	value  string
	getErr error
	setErr error
	writes int
}

func (r *totalDurationCircuitSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if key != SettingKeyTotalDurationCircuitBreakCount {
		return "", ErrSettingNotFound
	}
	return r.value, r.getErr
}

func (r *totalDurationCircuitSettingsRepo) Set(_ context.Context, key, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	r.value = value
	r.writes++
	return nil
}

func TestTotalDurationCircuitSettingsDefaultsAndConfiguredFallback(t *testing.T) {
	ctx := context.Background()
	defaults, err := NewSettingService(&totalDurationCircuitSettingsRepo{}, nil).GetTotalDurationCircuitBreakerSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, &TotalDurationCircuitBreakerSettings{MaxSlowRequests: 15, ThresholdSeconds: 180, WindowSeconds: 1800}, defaults)

	cfg := &config.Config{Gateway: config.GatewayConfig{
		TotalDurationCircuitBreakThresholdSeconds: 120,
		TotalDurationCircuitBreakCount:            8,
		TotalDurationCircuitBreakWindowSeconds:    600,
	}}
	fallback, err := NewSettingService(&totalDurationCircuitSettingsRepo{getErr: ErrSettingNotFound}, cfg).GetTotalDurationCircuitBreakerSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, &TotalDurationCircuitBreakerSettings{MaxSlowRequests: 7, ThresholdSeconds: 120, WindowSeconds: 600}, fallback)
}

func TestTotalDurationCircuitSettingsPersistAndRefreshAcrossInstances(t *testing.T) {
	ctx := context.Background()
	repo := &totalDurationCircuitSettingsRepo{}
	control := NewSettingService(repo, nil)
	gateway := NewSettingService(repo, nil)
	before, err := gateway.GetTotalDurationLatencyPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 16, before.CircuitBreakCount)

	for _, limit := range []int{7, 0, 9999, 15} {
		saved, err := control.UpdateTotalDurationCircuitBreakerSettings(ctx, limit)
		require.NoError(t, err)
		require.Equal(t, limit, saved.MaxSlowRequests)
		require.Equal(t, strconv.Itoa(limit+1), repo.value)
		policy, err := gateway.GetTotalDurationLatencyPolicy(ctx)
		require.NoError(t, err)
		require.Equal(t, limit+1, policy.CircuitBreakCount, "an already-running gateway must read each saved change immediately")
		reloaded, err := NewSettingService(repo, nil).GetTotalDurationCircuitBreakerSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, saved, reloaded, "a new process must retain the saved setting")
	}
}

func TestTotalDurationCircuitSettingsRejectInvalidAndFailedWrites(t *testing.T) {
	ctx := context.Background()
	repo := &totalDurationCircuitSettingsRepo{value: "16"}
	svc := NewSettingService(repo, nil)
	for _, invalid := range []int{-1, 10000} {
		_, err := svc.UpdateTotalDurationCircuitBreakerSettings(ctx, invalid)
		require.Error(t, err)
	}
	require.Zero(t, repo.writes)
	require.Equal(t, "16", repo.value)
	repo.setErr = errors.New("storage write failed")
	_, err := svc.UpdateTotalDurationCircuitBreakerSettings(ctx, 7)
	require.Error(t, err)
	require.Equal(t, "16", repo.value)
}

type totalDurationCircuitPolicyRecorder struct {
	staticFirstTokenLatencyStatsCache
	policies []TotalDurationLatencyPolicy
}

func (c *totalDurationCircuitPolicyRecorder) RecordSampleWithPolicy(_ context.Context, _ int64, _ string, _ int, policy TotalDurationLatencyPolicy) error {
	c.policies = append(c.policies, policy)
	return nil
}

func TestObserveTotalDurationLatencyReadsSavedPolicyAndFallsBackOnReadFailure(t *testing.T) {
	ctx := context.Background()
	repo := &totalDurationCircuitSettingsRepo{value: "8"}
	cache := &totalDurationCircuitPolicyRecorder{}
	svc := NewRateLimitService(nil, nil, nil, nil, nil)
	svc.SetSettingService(NewSettingService(repo, nil))
	svc.SetFirstTokenLatencyStatsCache(cache)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	durationMS := 180001
	usage := &UsageLog{RequestID: "billable-stream", RequestType: RequestTypeStream, Stream: true, ActualCost: 0.01, DurationMs: &durationMS}
	svc.ObserveTotalDurationLatency(ctx, account, usage)
	require.Equal(t, 8, cache.policies[0].CircuitBreakCount)
	repo.value = "4"
	svc.ObserveTotalDurationLatency(ctx, account, usage)
	require.Equal(t, 4, cache.policies[1].CircuitBreakCount)
	repo.getErr = errors.New("storage read failed")
	svc.ObserveTotalDurationLatency(ctx, account, usage)
	require.Equal(t, 16, cache.policies[2].CircuitBreakCount)
	require.Equal(t, 3*time.Minute, cache.policies[2].CircuitBreakThreshold)
	require.Zero(t, cache.recordedID, "the legacy record path must not replace the sample policy")
}
