package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterChannelMonitorsForDomain(t *testing.T) {
	monitors := []*ChannelMonitor{{ID: 10}, {ID: 11}, {ID: 12}}

	require.Len(t, filterChannelMonitorsForDomain(context.Background(), monitors), 3)
	legacyCtx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{Configured: true})
	require.Len(t, filterChannelMonitorsForDomain(legacyCtx, monitors), 3)
	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{
		Configured:        true,
		ChannelMonitorIDs: []int64{11},
	})
	filtered := filterChannelMonitorsForDomain(ctx, monitors)
	require.Len(t, filtered, 1)
	require.Equal(t, int64(11), filtered[0].ID)
}

func TestGetUserDetailRejectsMonitorOutsideDomainBrand(t *testing.T) {
	service := &ChannelMonitorService{}
	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{
		Configured:        true,
		ChannelMonitorIDs: []int64{11},
	})

	_, err := service.GetUserDetail(ctx, 12)
	require.ErrorIs(t, err, ErrChannelMonitorNotFound)
}

func TestEffectiveMonitorStatusRequiresConfirmedFailures(t *testing.T) {
	tests := []struct {
		name   string
		latest *ChannelMonitorLatest
		want   string
	}{
		{name: "no history", latest: nil, want: ""},
		{
			name:   "single transport error is degraded",
			latest: &ChannelMonitorLatest{Status: MonitorStatusError},
			want:   MonitorStatusDegraded,
		},
		{
			name:   "unconfirmed challenge failure is degraded",
			latest: &ChannelMonitorLatest{Status: MonitorStatusFailed},
			want:   MonitorStatusDegraded,
		},
		{
			name: "confirmed transport errors stay red",
			latest: &ChannelMonitorLatest{
				Status:                  MonitorStatusError,
				FailureThresholdReached: true,
			},
			want: MonitorStatusError,
		},
		{
			name: "confirmed challenge failures stay red",
			latest: &ChannelMonitorLatest{
				Status:                  MonitorStatusFailed,
				FailureThresholdReached: true,
			},
			want: MonitorStatusFailed,
		},
		{
			name: "success recovers immediately",
			latest: &ChannelMonitorLatest{
				Status:                  MonitorStatusOperational,
				FailureThresholdReached: false,
			},
			want: MonitorStatusOperational,
		},
		{
			name:   "slow success remains degraded",
			latest: &ChannelMonitorLatest{Status: MonitorStatusDegraded},
			want:   MonitorStatusDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveMonitorStatus(tt.latest); got != tt.want {
				t.Fatalf("effectiveMonitorStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildStatusSummaryUsesFailureConfirmationForAllModels(t *testing.T) {
	latest := map[string]*ChannelMonitorLatest{
		"primary": {Status: MonitorStatusError},
		"extra": {
			Status:                  MonitorStatusFailed,
			FailureThresholdReached: true,
		},
	}

	summary := buildStatusSummary(latest, nil, "primary", []string{"extra"})
	if summary.PrimaryStatus != MonitorStatusDegraded {
		t.Fatalf("primary status = %q, want degraded", summary.PrimaryStatus)
	}
	if len(summary.ExtraModels) != 1 || summary.ExtraModels[0].Status != MonitorStatusFailed {
		t.Fatalf("extra model status = %#v, want confirmed failure", summary.ExtraModels)
	}
}
