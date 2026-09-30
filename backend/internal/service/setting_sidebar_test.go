//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type sidebarRepoStub struct {
	settingPublicRepoStub
	keys []string
}

func (r *sidebarRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	r.keys = keys
	return r.settingPublicRepoStub.GetMultiple(ctx, keys)
}

func TestSettingService_GetSidebarSettings(t *testing.T) {
	r := &sidebarRepoStub{settingPublicRepoStub: settingPublicRepoStub{values: map[string]string{
		SettingKeyOpsMonitoringEnabled: "false", SettingKeyOpsRealtimeMonitoringEnabled: "false",
		SettingKeyOpsQueryModeDefault: "invalid", SettingKeyCustomMenuItems: `[{"id":"custom","visibility":"admin"}]`,
		SettingPaymentEnabled: "true", SettingKeySiteLogo: "large branding", "smtp_password": "secret",
	}}}
	s := NewSettingService(r, &config.Config{})
	settings, err := s.GetSidebarSettings(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{SettingKeyOpsMonitoringEnabled, SettingKeyOpsRealtimeMonitoringEnabled, SettingKeyOpsQueryModeDefault, SettingKeyCustomMenuItems, SettingPaymentEnabled}, r.keys)
	require.False(t, settings.OpsMonitoringEnabled)
	require.False(t, settings.OpsRealtimeMonitoringEnabled)
	require.Equal(t, "auto", settings.OpsQueryModeDefault)
	require.Equal(t, r.values[SettingKeyCustomMenuItems], settings.CustomMenuItems)
	require.True(t, settings.PaymentEnabled)

	r.values = nil
	settings, err = s.GetSidebarSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.OpsMonitoringEnabled)
	require.True(t, settings.OpsRealtimeMonitoringEnabled)
	require.False(t, settings.PaymentEnabled)
	r.err = errors.New("offline")
	_, err = s.GetSidebarSettings(context.Background())
	require.ErrorContains(t, err, "offline")
}
