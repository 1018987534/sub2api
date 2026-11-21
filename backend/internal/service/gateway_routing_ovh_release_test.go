package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutingOVHRestartDoesNotLeaveCooldown(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 9, 27, 39, 0, time.UTC)
	settings := packetLossSettings()
	settings.ICMPPauseMode = gatewayICMPObserve
	settings.PacketLossCooldownMinutes = 60
	settings.OVHGeoPercent = 100
	settings.Nodes = append(settings.Nodes, GatewayRoutingNodeSettings{ID: gatewayRoutingOVHNodeID, Origin: "https://ovh.example"})
	repo := newGatewayRoutingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	apply := func(service *SettingService, at time.Time, ready bool) *GatewayRoutingRuntime {
		signals := map[string]gatewayPacketLossSignal{
			"yt":                    {OriginHealthy: true, OriginRecovered: true, OriginSampleAt: at},
			"peer":                  {OriginHealthy: true, OriginRecovered: true, OriginSampleAt: at},
			gatewayRoutingOVHNodeID: {OriginHealthy: ready, OriginRecovered: ready, OriginUnhealthy: !ready, OriginSampleAt: at},
		}
		runtime := packetLossRuntime(settings)
		service.applyGatewayPacketLossProtection(ctx, settings, runtime, signals, at)
		applyGatewayRoutingOVHShare(settings, runtime)
		return runtime
	}
	failed := apply(svc, now, false).Nodes[2]
	t.Logf("restart_unready: geo=%d status=%s cooldown=%v", *failed.OVHGeoEffectivePercent, failed.Status, failed.PacketLossPausedUntil != nil)
	recovered := apply(svc, now.Add(20*time.Second), true).Nodes[2]
	t.Logf("ready_after_20s: geo=%d status=%s cooldown=%v", *recovered.OVHGeoEffectivePercent, recovered.Status, recovered.PacketLossPausedUntil != nil)
	restarted := apply(NewSettingService(repo, &config.Config{}), now.Add(30*time.Second), true).Nodes[2]
	t.Logf("control_restarted: geo=%d cooldown=%v", *restarted.OVHGeoEffectivePercent, restarted.PacketLossPausedUntil != nil)
	states := map[string]gatewayPacketLossPause{}
	raw, _ := repo.GetValue(ctx, gatewayPacketLossStateKey)
	if raw != "" {
		require.NoError(t, json.Unmarshal([]byte(raw), &states))
	}
	_, stored := states[settings.MonitorURL+"|"+gatewayRoutingOVHNodeID+"|https://ovh.example"]
	t.Logf("persistent_ovh_pause=%v", stored)
	require.Zero(t, *failed.OVHGeoEffectivePercent, "unready origin must not receive traffic")
	require.Nil(t, failed.PacketLossPausedUntil, "restart must not create a fixed OVH cooldown")
	require.Equal(t, 100, *recovered.OVHGeoEffectivePercent, "ready OVH must resume before the old one-hour deadline")
	require.Equal(t, 100, *restarted.OVHGeoEffectivePercent)
	require.False(t, stored)
}

func TestGatewayRoutingOVHExistingOriginPauseIsReleased(t *testing.T) {
	for _, mode := range []string{gatewayICMPObserve, gatewayICMPLegacy} {
		t.Run(mode, func(t *testing.T) {
			ctx, now := context.Background(), time.Now().UTC()
			settings := packetLossSettings()
			settings.ICMPPauseMode = mode
			settings.OVHGeoPercent = 100
			settings.Nodes = append(settings.Nodes, GatewayRoutingNodeSettings{ID: gatewayRoutingOVHNodeID, Origin: "https://ovh.example"})
			repo := newGatewayRoutingRepoStub()
			key := settings.MonitorURL + "|" + gatewayRoutingOVHNodeID + "|https://ovh.example"
			raw, err := json.Marshal(map[string]gatewayPacketLossPause{key: {Until: now.Add(time.Hour), SampleAt: now.Add(-time.Minute), OriginProbe: true}})
			require.NoError(t, err)
			require.NoError(t, repo.Set(ctx, gatewayPacketLossStateKey, string(raw)))
			svc := NewSettingService(repo, &config.Config{})
			runtime := packetLossRuntime(settings)
			svc.applyGatewayPacketLossProtection(ctx, settings, runtime, map[string]gatewayPacketLossSignal{
				"yt":                    {OriginHealthy: true, OriginSampleAt: now},
				gatewayRoutingOVHNodeID: {OriginHealthy: true, OriginRecovered: true, OriginSampleAt: now},
			}, now)
			applyGatewayRoutingOVHShare(settings, runtime)
			require.Equal(t, 100, *runtime.Nodes[2].OVHGeoEffectivePercent)
			require.Nil(t, runtime.Nodes[2].PacketLossPausedUntil)
			states := map[string]gatewayPacketLossPause{}
			value, err := repo.GetValue(ctx, gatewayPacketLossStateKey)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal([]byte(value), &states))
			require.NotContains(t, states, key)
		})
	}
}

func TestGatewayRoutingOVHReadinessDoesNotOverrideOtherProtection(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	settings := packetLossSettings()
	settings.ICMPPauseMode = gatewayICMPObserve
	settings.OVHGeoPercent = 100
	settings.Nodes = append(settings.Nodes, GatewayRoutingNodeSettings{ID: gatewayRoutingOVHNodeID, Origin: "https://ovh.example"})
	for _, reason := range []string{"traffic_threshold", "monitor_stale"} {
		t.Run(reason, func(t *testing.T) {
			runtime := packetLossRuntime(settings)
			applyAutoDisabledGatewayRoutingNode(&runtime.Nodes[2], reason)
			svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
			svc.applyGatewayPacketLossProtection(ctx, settings, runtime, map[string]gatewayPacketLossSignal{
				gatewayRoutingOVHNodeID: {OriginHealthy: true, OriginRecovered: true, OriginSampleAt: now},
			}, now)
			applyGatewayRoutingOVHShare(settings, runtime)
			require.Zero(t, *runtime.Nodes[2].OVHGeoEffectivePercent)
			require.Equal(t, reason, runtime.Nodes[2].AutoDisabledReason)
		})
	}
	t.Run("state unavailable does not resurrect origin cooldown", func(t *testing.T) {
		repo := &packetLossFailingRepo{gatewayRoutingRepoStub: newGatewayRoutingRepoStub(), failRead: true}
		svc := NewSettingService(repo, &config.Config{})
		old := packetLossRuntime(settings)
		deadline := now.Add(time.Hour)
		old.Nodes[2].PacketLossPausedUntil = &deadline
		applyAutoDisabledGatewayRoutingNode(&old.Nodes[2], "packet_loss")
		svc.gatewayRoutingProtectedRuntime = old
		runtime := packetLossRuntime(settings)
		svc.applyGatewayPacketLossProtection(ctx, settings, runtime, map[string]gatewayPacketLossSignal{
			gatewayRoutingOVHNodeID: {OriginHealthy: true, OriginRecovered: true, OriginSampleAt: now},
		}, now)
		applyGatewayRoutingOVHShare(settings, runtime)
		require.Equal(t, 100, *runtime.Nodes[2].OVHGeoEffectivePercent)
		require.Nil(t, runtime.Nodes[2].PacketLossPausedUntil)
	})
	t.Run("legacy ICMP retains explicit pause", func(t *testing.T) {
		settings.ICMPPauseMode = gatewayICMPLegacy
		svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
		signals := map[string]gatewayPacketLossSignal{
			"yt":                    {Fresh: true, Recovered: true, SampleAt: now, OriginHealthy: true, OriginSampleAt: now},
			gatewayRoutingOVHNodeID: {Fresh: true, Severe: true, SampleAt: now, OriginHealthy: true, OriginRecovered: true, OriginSampleAt: now},
		}
		runtime := packetLossRuntime(settings)
		svc.applyGatewayPacketLossProtection(ctx, settings, runtime, signals, now)
		require.NotNil(t, runtime.Nodes[2].PacketLossPausedUntil)
		require.Equal(t, "icmp_packet_loss", runtime.Nodes[2].ProtectionCause)
		require.Equal(t, "auto_disabled_packet_loss", runtime.Nodes[2].Status)
		signals[gatewayRoutingOVHNodeID] = gatewayPacketLossSignal{OriginHealthy: true, OriginRecovered: true, OriginSampleAt: now.Add(20 * time.Second)}
		runtime = packetLossRuntime(settings)
		svc.applyGatewayPacketLossProtection(ctx, settings, runtime, signals, now.Add(20*time.Second))
		applyGatewayRoutingOVHShare(settings, runtime)
		require.Zero(t, *runtime.Nodes[2].OVHGeoEffectivePercent)
		require.NotNil(t, runtime.Nodes[2].PacketLossPausedUntil)
	})
}

func TestGatewayRoutingOriginFailureHasOwnCause(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	settings := packetLossSettings()
	settings.ICMPPauseMode = gatewayICMPObserve
	svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
	runtime := packetLossRuntime(settings)
	svc.applyGatewayPacketLossProtection(ctx, settings, runtime, map[string]gatewayPacketLossSignal{
		"yt":   {Fresh: true, Recovered: true, OriginUnhealthy: true, OriginSampleAt: now},
		"peer": {OriginHealthy: true, OriginRecovered: true, OriginSampleAt: now},
	}, now)
	require.Equal(t, "origin_health", runtime.Nodes[0].ProtectionCause)
	require.Equal(t, "auto_disabled_origin_health", runtime.Nodes[0].Status)
	require.NotNil(t, runtime.Nodes[0].PacketLossPausedUntil)
}
