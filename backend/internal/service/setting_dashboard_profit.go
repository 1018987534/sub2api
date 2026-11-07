package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const SettingKeyDashboardMonthlyServerCostUSD = "dashboard_monthly_server_cost_usd"

type DashboardProfitSettings struct {
	MonthlyServerCostUSD float64 `json:"monthly_server_cost_usd"`
	DailyServerCostUSD   float64 `json:"daily_server_cost_usd"`
	MonthDays            int     `json:"month_days"`
}

func dashboardProfitSettingsForMonth(monthly float64, at time.Time) *DashboardProfitSettings {
	days := time.Date(at.Year(), at.Month()+1, 0, 0, 0, 0, 0, at.Location()).Day()
	return &DashboardProfitSettings{
		MonthlyServerCostUSD: monthly,
		DailyServerCostUSD:   monthly / float64(days),
		MonthDays:            days,
	}
}

func validDashboardMonthlyServerCost(monthly float64) bool {
	return !math.IsNaN(monthly) && !math.IsInf(monthly, 0) && monthly >= 0
}

// Read shared settings directly so saved expenses survive reloads and restarts.
func (s *SettingService) GetDashboardProfitSettings(ctx context.Context) (*DashboardProfitSettings, error) {
	monthly := 0.0
	if s != nil && s.settingRepo != nil {
		dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{SettingKeyDashboardMonthlyServerCostUSD})
		if err != nil {
			return nil, fmt.Errorf("get dashboard profit settings: %w", err)
		}
		if raw := strings.TrimSpace(values[SettingKeyDashboardMonthlyServerCostUSD]); raw != "" {
			monthly, err = strconv.ParseFloat(raw, 64)
			if err != nil || !validDashboardMonthlyServerCost(monthly) {
				return nil, fmt.Errorf("invalid stored dashboard monthly server cost")
			}
		}
	}
	return dashboardProfitSettingsForMonth(monthly, timezone.Now()), nil
}

func (s *SettingService) UpdateDashboardProfitSettings(ctx context.Context, monthly float64) (*DashboardProfitSettings, error) {
	if !validDashboardMonthlyServerCost(monthly) {
		return nil, infraerrors.BadRequest("DASHBOARD_MONTHLY_SERVER_COST_INVALID", "monthly_server_cost_usd must be a finite non-negative amount")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("dashboard profit settings storage is unavailable")
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyDashboardMonthlyServerCostUSD: strconv.FormatFloat(monthly, 'f', -1, 64),
	}); err != nil {
		return nil, fmt.Errorf("save dashboard profit settings: %w", err)
	}
	return dashboardProfitSettingsForMonth(monthly, timezone.Now()), nil
}
