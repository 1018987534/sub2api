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

const (
	SettingKeyDashboardMonthlyServerCostUSD     = "dashboard_monthly_server_cost_usd"
	SettingKeyDashboardWithdrawalFeeRatePercent = "dashboard_withdrawal_fee_rate_percent"
	defaultDashboardWithdrawalFeeRatePercent    = 1.8
)

type DashboardProfitSettings struct {
	MonthlyServerCostUSD     float64 `json:"monthly_server_cost_usd"`
	DailyServerCostUSD       float64 `json:"daily_server_cost_usd"`
	MonthDays                int     `json:"month_days"`
	WithdrawalFeeRatePercent float64 `json:"withdrawal_fee_rate_percent"`
}

type DashboardProfitSettingsUpdate struct {
	MonthlyServerCostUSD     *float64 `json:"monthly_server_cost_usd"`
	WithdrawalFeeRatePercent *float64 `json:"withdrawal_fee_rate_percent"`
}

func dashboardProfitSettingsForMonth(monthly float64, at time.Time) *DashboardProfitSettings {
	days := time.Date(at.Year(), at.Month()+1, 0, 0, 0, 0, 0, at.Location()).Day()
	return &DashboardProfitSettings{
		MonthlyServerCostUSD:     monthly,
		DailyServerCostUSD:       monthly / float64(days),
		MonthDays:                days,
		WithdrawalFeeRatePercent: defaultDashboardWithdrawalFeeRatePercent,
	}
}

func validDashboardMonthlyServerCost(monthly float64) bool {
	return !math.IsNaN(monthly) && !math.IsInf(monthly, 0) && monthly >= 0
}

func validDashboardWithdrawalFeeRate(rate float64) bool {
	return !math.IsNaN(rate) && !math.IsInf(rate, 0) && rate >= 0 && rate <= 100
}

// Read shared settings directly so saved expenses survive reloads and restarts.
func (s *SettingService) GetDashboardProfitSettings(ctx context.Context) (*DashboardProfitSettings, error) {
	monthly := 0.0
	rate := defaultDashboardWithdrawalFeeRatePercent
	if s != nil && s.settingRepo != nil {
		dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{SettingKeyDashboardMonthlyServerCostUSD, SettingKeyDashboardWithdrawalFeeRatePercent})
		if err != nil {
			return nil, fmt.Errorf("get dashboard profit settings: %w", err)
		}
		if raw := strings.TrimSpace(values[SettingKeyDashboardMonthlyServerCostUSD]); raw != "" {
			monthly, err = strconv.ParseFloat(raw, 64)
			if err != nil || !validDashboardMonthlyServerCost(monthly) {
				return nil, fmt.Errorf("invalid stored dashboard monthly server cost")
			}
		}
		if raw := strings.TrimSpace(values[SettingKeyDashboardWithdrawalFeeRatePercent]); raw != "" {
			rate, err = strconv.ParseFloat(raw, 64)
			if err != nil || !validDashboardWithdrawalFeeRate(rate) {
				return nil, fmt.Errorf("invalid stored dashboard withdrawal fee rate")
			}
		}
	}
	settings := dashboardProfitSettingsForMonth(monthly, timezone.Now())
	settings.WithdrawalFeeRatePercent = rate
	return settings, nil
}

func (s *SettingService) UpdateDashboardProfitSettings(ctx context.Context, update DashboardProfitSettingsUpdate) (*DashboardProfitSettings, error) {
	if update.MonthlyServerCostUSD == nil && update.WithdrawalFeeRatePercent == nil {
		return nil, infraerrors.BadRequest("DASHBOARD_PROFIT_SETTINGS_EMPTY", "provide monthly_server_cost_usd or withdrawal_fee_rate_percent")
	}
	if update.MonthlyServerCostUSD != nil && !validDashboardMonthlyServerCost(*update.MonthlyServerCostUSD) {
		return nil, infraerrors.BadRequest("DASHBOARD_MONTHLY_SERVER_COST_INVALID", "monthly_server_cost_usd must be a finite non-negative amount")
	}
	if update.WithdrawalFeeRatePercent != nil && !validDashboardWithdrawalFeeRate(*update.WithdrawalFeeRatePercent) {
		return nil, infraerrors.BadRequest("DASHBOARD_WITHDRAWAL_FEE_RATE_INVALID", "withdrawal_fee_rate_percent must be a finite percentage between 0 and 100")
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("dashboard profit settings storage is unavailable")
	}
	values := make(map[string]string)
	if update.MonthlyServerCostUSD != nil {
		values[SettingKeyDashboardMonthlyServerCostUSD] = strconv.FormatFloat(*update.MonthlyServerCostUSD, 'f', -1, 64)
	}
	if update.WithdrawalFeeRatePercent != nil {
		values[SettingKeyDashboardWithdrawalFeeRatePercent] = strconv.FormatFloat(*update.WithdrawalFeeRatePercent, 'f', -1, 64)
	}
	if err := s.settingRepo.SetMultiple(ctx, values); err != nil {
		return nil, fmt.Errorf("save dashboard profit settings: %w", err)
	}
	return s.GetDashboardProfitSettings(ctx)
}
