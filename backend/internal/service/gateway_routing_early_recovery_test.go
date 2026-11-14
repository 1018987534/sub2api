package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGatewayPacketLossEarlyRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	for _, originPause := range []bool{false, true} {
		cause := "icmp"
		if originPause {
			cause = "origin"
		}
		for _, scenario := range []string{"confirmed recovery", "icmp only", "origin only", "stale icmp", "old icmp sample", "old origin sample", "severe loss", "origin failure", "quota protection", "zero target"} {
			t.Run(cause+"/"+scenario, func(t *testing.T) {
				repo := newGatewayRoutingRepoStub()
				svc := NewSettingService(repo, &config.Config{})
				settings := packetLossSettings()
				settings.PacketLossCooldownMinutes = 60
				failed := gatewayPacketLossSignal{Fresh: true, Severe: true, SampleAt: now}
				if originPause {
					failed = gatewayPacketLossSignal{OriginUnhealthy: true, OriginSampleAt: now}
				}
				signals := map[string]gatewayPacketLossSignal{
					"yt": failed, "peer": {Fresh: true, Recovered: true, SampleAt: now},
				}
				runtime := packetLossRuntime(settings)
				svc.applyGatewayPacketLossProtection(ctx, settings, runtime, signals, now)
				require.Zero(t, runtime.Nodes[0].EffectiveWeight)
				require.Equal(t, now.Add(time.Hour), *runtime.Nodes[0].PacketLossPausedUntil)

				// Recovery must also work from the persisted pause after a restart.
				svc = NewSettingService(repo, &config.Config{})
				recoveredAt := now.Add(3 * time.Minute)
				signal := gatewayPacketLossSignal{
					Fresh: true, Recovered: true, SampleAt: recoveredAt,
					OriginHealthy: true, OriginRecovered: true, OriginSampleAt: recoveredAt,
				}
				switch scenario {
				case "icmp only":
					signal.OriginRecovered = false
				case "origin only":
					signal.Recovered = false
				case "stale icmp":
					signal.Fresh = false
				case "old icmp sample":
					signal.SampleAt = now
				case "old origin sample":
					signal.OriginSampleAt = now
				case "severe loss":
					signal.Severe, signal.Recovered = true, false
				case "origin failure":
					signal.OriginUnhealthy, signal.OriginRecovered = true, false
				case "zero target":
					settings.Nodes[0].TargetWeight = 0
				}
				signals["yt"] = signal
				runtime = packetLossRuntime(settings)
				if scenario == "quota protection" {
					applyAutoDisabledGatewayRoutingNode(&runtime.Nodes[0], "traffic_threshold")
				}
				svc.applyGatewayPacketLossProtection(ctx, settings, runtime, signals, recoveredAt)
				node := runtime.Nodes[0]
				t.Logf("pause=%s scenario=%s elapsed=3m cooldown=60m effective=%d paused=%t status=%s", cause, scenario, node.EffectiveWeight, node.PacketLossPausedUntil != nil, node.Status)
				switch scenario {
				case "confirmed recovery":
					require.Equal(t, 70, node.EffectiveWeight)
					require.False(t, node.AutoDisabled)
					require.Nil(t, node.PacketLossPausedUntil)
					require.Equal(t, "{}", repo.values[gatewayPacketLossStateKey])
				case "quota protection":
					require.Zero(t, node.EffectiveWeight)
					require.Equal(t, "traffic_threshold", node.AutoDisabledReason)
					require.Nil(t, node.PacketLossPausedUntil)
				case "zero target":
					require.Zero(t, node.EffectiveWeight)
					require.False(t, node.AutoDisabled)
					require.Nil(t, node.PacketLossPausedUntil)
				default:
					require.Zero(t, node.EffectiveWeight)
					require.True(t, node.AutoDisabled)
					require.NotNil(t, node.PacketLossPausedUntil)
				}
				require.Equal(t, settings.Nodes[0].TargetWeight, node.TargetWeight)
			})
		}
	}
}
