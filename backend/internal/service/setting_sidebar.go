package service

import (
	"context"
	"fmt"
)

type SidebarSettings struct {
	OpsMonitoringEnabled         bool
	OpsRealtimeMonitoringEnabled bool
	OpsQueryModeDefault          string
	CustomMenuItems              string
	PaymentEnabled               bool
}

func (s *SettingService) GetSidebarSettings(ctx context.Context) (*SidebarSettings, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyOpsMonitoringEnabled, SettingKeyOpsRealtimeMonitoringEnabled,
		SettingKeyOpsQueryModeDefault, SettingKeyCustomMenuItems, SettingPaymentEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("get sidebar settings: %w", err)
	}
	return &SidebarSettings{
		OpsMonitoringEnabled:         !isFalseSettingValue(values[SettingKeyOpsMonitoringEnabled]),
		OpsRealtimeMonitoringEnabled: !isFalseSettingValue(values[SettingKeyOpsRealtimeMonitoringEnabled]),
		OpsQueryModeDefault:          string(ParseOpsQueryMode(values[SettingKeyOpsQueryModeDefault])),
		CustomMenuItems:              values[SettingKeyCustomMenuItems],
		PaymentEnabled:               values[SettingPaymentEnabled] == "true",
	}, nil
}
