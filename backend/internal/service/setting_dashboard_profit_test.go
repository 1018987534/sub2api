package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dashboardProfitSettingsRepo struct {
	SettingRepository
	values map[string]string
	getErr error
	setErr error
	writes int
}

func (r *dashboardProfitSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, r.getErr
}

func (r *dashboardProfitSettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range values {
		r.values[key] = value
	}
	r.writes++
	return nil
}

func TestDashboardProfitSettingsActualMonthDays(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, tc := range []struct {
		name string
		at   time.Time
		days int
	}{
		{"February", time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), 28},
		{"leap February", time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC), 29},
		{"November", time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), 30},
		{"December", time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC), 31},
		{"local month boundary", time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC).In(shanghai), 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dashboardProfitSettingsForMonth(600, tc.at)
			require.Equal(t, tc.days, got.MonthDays)
			require.InDelta(t, 600/float64(tc.days), got.DailyServerCostUSD, 1e-12)
			require.InDelta(t, 600, got.DailyServerCostUSD*float64(tc.days), 1e-9, "daily allocations must not be rounded before profit calculation")
		})
	}
}

func TestDashboardProfitSettingsPersistsAndZeroDisables(t *testing.T) {
	ctx := context.Background()
	repo := &dashboardProfitSettingsRepo{}
	control := NewSettingService(repo, nil)
	initial, err := control.GetDashboardProfitSettings(ctx)
	require.NoError(t, err)
	require.Zero(t, initial.MonthlyServerCostUSD)
	for _, monthly := range []float64{600, 620.5, 0} {
		saved, err := control.UpdateDashboardProfitSettings(ctx, monthly)
		require.NoError(t, err)
		reloaded, err := NewSettingService(repo, nil).GetDashboardProfitSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, saved, reloaded)
		require.Equal(t, monthly, saved.MonthlyServerCostUSD)
	}
	require.Equal(t, 3, repo.writes)
	require.Equal(t, "0", repo.values[SettingKeyDashboardMonthlyServerCostUSD])
}

func TestDashboardProfitSettingsRejectsInvalidAmountsAndStorageFailures(t *testing.T) {
	ctx := context.Background()
	repo := &dashboardProfitSettingsRepo{values: map[string]string{SettingKeyDashboardMonthlyServerCostUSD: "600"}}
	svc := NewSettingService(repo, nil)
	for _, amount := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := svc.UpdateDashboardProfitSettings(ctx, amount)
		require.Error(t, err)
	}
	require.Zero(t, repo.writes)
	for _, stored := range []string{"-1", "NaN", "+Inf", "invalid"} {
		repo.values[SettingKeyDashboardMonthlyServerCostUSD] = stored
		_, err := svc.GetDashboardProfitSettings(ctx)
		require.Error(t, err)
	}
	repo.getErr = errors.New("read unavailable")
	_, err := svc.GetDashboardProfitSettings(ctx)
	require.ErrorContains(t, err, "read unavailable")
	repo.setErr = errors.New("write unavailable")
	_, err = svc.UpdateDashboardProfitSettings(ctx, 100)
	require.ErrorContains(t, err, "write unavailable")
}
