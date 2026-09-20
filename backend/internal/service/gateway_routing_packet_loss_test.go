package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func packetLossProbes(now time.Time, patterns ...[]float64) gatewayPingResponse {
	var payload gatewayPingResponse
	for i, values := range patterns {
		payload.Data.Tasks = append(payload.Data.Tasks, gatewayPingTask{ID: i + 1, Type: "icmp", Interval: 60})
		for j, value := range values {
			payload.Data.Records = append(payload.Data.Records, gatewayPingRecord{TaskID: i + 1, Time: now.Add(-time.Duration(j) * time.Minute), Value: &value, Client: "node"})
		}
	}
	return payload
}

func TestGatewayPacketLossProbeWindows(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	good := []float64{0, 1, 160, 160, 160, 160}
	double := []float64{-1, -1, 160, 160, 160, 160}
	one := []float64{-1, 160, 160, 160, 160, 160}
	bursts := []float64{-1, 160, -1, 160, 160, -1}
	cases := []struct {
		name                     string
		patterns                 [][]float64
		fresh, severe, recovered bool
	}{
		{"zero is a successful sub-millisecond ping", [][]float64{good, good, good}, true, false, true},
		{"isolated loss", [][]float64{one, one, one}, true, false, false},
		{"one target down", [][]float64{double, good, good}, true, false, false},
		{"two of three consecutive failures", [][]float64{double, double, good}, true, true, false},
		{"two of four is not a majority", [][]float64{double, double, good, good}, true, false, false},
		{"sustained forty percent", [][]float64{bursts, bursts, good}, true, true, false},
		{"first bad window needs confirmation", [][]float64{{-1, 160, -1, 160, 160, 160}, {-1, 160, -1, 160, 160, 160}}, true, false, false},
		{"old losses with three good rounds", [][]float64{{160, 160, 160, -1, -1, -1}, {160, 160, 160, -1, -1, -1}}, true, false, true},
		{"one sample is insufficient", [][]float64{{-1}, {-1}, {-1}}, false, false, false},
		{"single target insufficient", [][]float64{double}, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signal := evaluateGatewayPacketLoss(packetLossProbes(now, tc.patterns...), "node", now)
			require.Equal(t, tc.fresh, signal.Fresh)
			require.Equal(t, tc.severe, signal.Severe)
			require.Equal(t, tc.recovered, signal.Recovered)
		})
	}
	for _, mutation := range []string{"stale", "future", "gap", "duplicate", "null", "wrong client", "non ICMP"} {
		t.Run(mutation, func(t *testing.T) {
			payload := packetLossProbes(now, double, double)
			for i := range payload.Data.Records {
				r := &payload.Data.Records[i]
				switch mutation {
				case "stale":
					r.Time = r.Time.Add(-10 * time.Minute)
				case "future":
					r.Time = r.Time.Add(20 * time.Minute)
				case "gap":
					if !r.Time.Equal(now) {
						r.Time = r.Time.Add(-10 * time.Minute)
					}
				case "duplicate":
					r.Time = now
				case "null":
					r.Value = nil
				case "wrong client":
					r.Client = "another-node"
				case "non ICMP":
					payload.Data.Tasks[0].Type = "tcp"
				}
			}
			signal := evaluateGatewayPacketLoss(payload, "node", now)
			require.False(t, signal.Fresh)
			require.False(t, signal.Severe)
		})
	}
}

func packetLossSettings() *GatewayRoutingSettings {
	s := DefaultGatewayRoutingSettings()
	s.Nodes = []GatewayRoutingNodeSettings{
		{ID: "yt", Origin: "https://yt.example", TargetWeight: 70},
		{ID: "peer", Origin: "https://peer.example", TargetWeight: 30},
	}
	return s
}

func packetLossRuntime(settings *GatewayRoutingSettings) *GatewayRoutingRuntime {
	r := &GatewayRoutingRuntime{}
	for _, node := range settings.Nodes {
		r.Nodes = append(r.Nodes, GatewayRoutingNodeRuntime{ID: node.ID, Origin: node.Origin, TargetWeight: node.TargetWeight, EffectiveWeight: node.TargetWeight, Status: "active"})
	}
	return r
}

func TestGatewayPacketLossDurableCooldownAndRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	repo := newGatewayRoutingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	settings := packetLossSettings()
	settings.PacketLossCooldownMinutes = 7
	signals := map[string]gatewayPacketLossSignal{
		"yt":   {Fresh: true, Severe: true, Percent: 40, SampleAt: now},
		"peer": {Fresh: true, Recovered: true, SampleAt: now},
	}
	run := func(svc *SettingService, when time.Time) *GatewayRoutingRuntime {
		r := packetLossRuntime(settings)
		svc.applyGatewayPacketLossProtection(ctx, settings, r, signals, when)
		return r
	}
	r := run(svc, now)
	require.Equal(t, "auto_disabled_packet_loss", r.Nodes[0].Status)
	require.Zero(t, r.Nodes[0].EffectiveWeight)
	require.Equal(t, 70, r.Nodes[0].TargetWeight)
	require.Equal(t, now.Add(7*time.Minute), *r.Nodes[0].PacketLossPausedUntil)
	require.Equal(t, 30, r.Nodes[1].EffectiveWeight)
	// Polling an identical sample does not move the deadline.
	r = run(svc, now.Add(30*time.Second))
	require.Equal(t, now.Add(7*time.Minute), *r.Nodes[0].PacketLossPausedUntil)
	r = run(svc, now.Add(7*time.Minute))
	require.Equal(t, now.Add(7*time.Minute), *r.Nodes[0].PacketLossPausedUntil)
	require.Equal(t, "awaiting_recovery", r.Nodes[0].PacketLossState)
	// A fresh service instance reads the same deadline after a control restart.
	svc = NewSettingService(repo, &config.Config{})
	signals["yt"] = gatewayPacketLossSignal{Fresh: true, Recovered: true, SampleAt: now.Add(time.Minute)}
	r = run(svc, now.Add(time.Minute))
	require.Zero(t, r.Nodes[0].EffectiveWeight)
	// A monitoring outage cannot masquerade as network recovery.
	delete(signals, "yt")
	r = run(svc, now.Add(7*time.Minute))
	require.Equal(t, "awaiting_recovery", r.Nodes[0].PacketLossState)
	require.Zero(t, r.Nodes[0].EffectiveWeight)
	signals["yt"] = gatewayPacketLossSignal{Fresh: true, Severe: true, SampleAt: now.Add(8 * time.Minute)}
	r = run(svc, now.Add(8*time.Minute))
	require.Equal(t, now.Add(15*time.Minute), *r.Nodes[0].PacketLossPausedUntil)
	signals["yt"] = gatewayPacketLossSignal{Fresh: true, Recovered: true, SampleAt: now.Add(15 * time.Minute)}
	r = run(svc, now.Add(15*time.Minute))
	require.Equal(t, 70, r.Nodes[0].EffectiveWeight)
	require.Nil(t, r.Nodes[0].PacketLossPausedUntil)
	require.Equal(t, "{}", repo.values[gatewayPacketLossStateKey])
}

func TestGatewayPacketLossPeerAndExistingProtection(t *testing.T) {
	for _, scenario := range []string{"all bad", "peer disabled", "peer stale", "peer quota", "peer paused", "overflow target zero", "manual disabled", "quota priority", "disabled switch", "origin changed", "monitor changed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, now := context.Background(), time.Now().UTC()
			repo := newGatewayRoutingRepoStub()
			svc := NewSettingService(repo, &config.Config{})
			settings := packetLossSettings()
			signals := map[string]gatewayPacketLossSignal{"yt": {Fresh: true, Severe: true}, "peer": {Fresh: true, Recovered: true}}
			r := packetLossRuntime(settings)
			if scenario == "quota priority" || scenario == "disabled switch" || scenario == "origin changed" || scenario == "monitor changed" {
				svc.applyGatewayPacketLossProtection(ctx, settings, r, signals, now)
				require.True(t, r.Nodes[0].AutoDisabled)
			}
			switch scenario {
			case "all bad":
				signals["peer"] = signals["yt"]
			case "peer disabled":
				settings.Nodes[1].TargetWeight = 0
				r.Nodes[1].EffectiveWeight = 0
			case "peer stale":
				r.Nodes[1].MonitorStale = true
			case "peer quota":
				applyAutoDisabledGatewayRoutingNode(&r.Nodes[1], "traffic_threshold")
			case "peer paused":
				repo.values[gatewayPacketLossStateKey] = `{"` + settings.MonitorURL + `|peer|https://peer.example":{"until":"` + now.Add(time.Minute).Format(time.RFC3339Nano) + `"}}`
			case "overflow target zero":
				settings.Nodes[0].TargetWeight = 0
				settings.OverflowNodeID = "yt"
			case "manual disabled":
				settings.Nodes[0].TargetWeight = 0
			case "quota priority":
				applyAutoDisabledGatewayRoutingNode(&r.Nodes[0], "traffic_threshold")
			case "disabled switch":
				settings.PacketLossProtectionEnabled = false
			case "origin changed":
				settings.Nodes[0].Origin = "https://new.example"
				signals["yt"] = gatewayPacketLossSignal{}
			case "monitor changed":
				settings.MonitorURL = "https://new-monitor.example"
				signals["yt"] = gatewayPacketLossSignal{}
			}
			svc.applyGatewayPacketLossProtection(ctx, settings, r, signals, now)
			if scenario == "overflow target zero" || scenario == "quota priority" {
				require.True(t, r.Nodes[0].AutoDisabled)
				require.Zero(t, r.Nodes[0].EffectiveWeight)
				if scenario == "quota priority" {
					require.Equal(t, "traffic_threshold", r.Nodes[0].AutoDisabledReason)
				}
			} else {
				require.False(t, r.Nodes[0].AutoDisabled)
			}
		})
	}
}

type packetLossFailingRepo struct {
	*gatewayRoutingRepoStub
	failRead, failWrite bool
}

func (r *packetLossFailingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if key == gatewayPacketLossStateKey && r.failRead {
		return "", errors.New("state read unavailable")
	}
	return r.gatewayRoutingRepoStub.GetValue(ctx, key)
}
func (r *packetLossFailingRepo) Set(ctx context.Context, key, value string) error {
	if key == gatewayPacketLossStateKey && r.failWrite {
		return errors.New("state write unavailable")
	}
	return r.gatewayRoutingRepoStub.Set(ctx, key, value)
}

func TestGatewayPacketLossStateStorageFailure(t *testing.T) {
	for _, readFailure := range []bool{false, true} {
		t.Run(map[bool]string{true: "read", false: "write"}[readFailure], func(t *testing.T) {
			ctx, now := context.Background(), time.Now().UTC()
			repo := &packetLossFailingRepo{gatewayRoutingRepoStub: newGatewayRoutingRepoStub()}
			svc := NewSettingService(repo, &config.Config{})
			settings := packetLossSettings()
			signals := map[string]gatewayPacketLossSignal{"yt": {Fresh: true, Severe: true}, "peer": {Fresh: true, Recovered: true}}
			repo.failRead, repo.failWrite = readFailure, !readFailure
			r := packetLossRuntime(settings)
			svc.applyGatewayPacketLossProtection(ctx, settings, r, signals, now)
			require.False(t, r.Nodes[0].AutoDisabled)
			require.Equal(t, "state_unavailable", r.Nodes[0].PacketLossState)
			repo.failRead, repo.failWrite = false, false
			svc.applyGatewayPacketLossProtection(ctx, settings, r, signals, now)
			require.True(t, r.Nodes[0].AutoDisabled)
			svc.gatewayRoutingRuntimeCache.Store(&cachedGatewayRoutingRuntime{runtime: cloneGatewayRoutingRuntime(r)})
			repo.failRead, repo.failWrite = readFailure, !readFailure
			signals["yt"] = gatewayPacketLossSignal{Fresh: true, Recovered: true}
			r = packetLossRuntime(settings)
			svc.applyGatewayPacketLossProtection(ctx, settings, r, signals, now.Add(10*time.Minute))
			require.True(t, r.Nodes[0].AutoDisabled)
			require.Zero(t, r.Nodes[0].EffectiveWeight)
		})
	}
}

func TestGatewayPacketLossSettingsValidation(t *testing.T) {
	var settings GatewayRoutingSettings
	require.NoError(t, json.Unmarshal([]byte(`{}`), &settings))
	require.True(t, settings.PacketLossProtectionEnabled)
	require.Equal(t, 5, settings.PacketLossCooldownMinutes)
	for _, minutes := range []int{-1, 0, 121} {
		settings.PacketLossCooldownMinutes = minutes
		require.ErrorContains(t, normalizeAndValidateGatewayRoutingSettings(&settings), "packet_loss_cooldown_minutes")
	}
	for _, minutes := range []int{1, 5, 120} {
		settings.PacketLossCooldownMinutes = minutes
		require.NoError(t, normalizeAndValidateGatewayRoutingSettings(&settings))
	}
}

func TestGatewayPacketLossRuntimeMonitorOutageAfterRestart(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	repo := newGatewayRoutingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	settings := packetLossSettings()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	settings.MonitorURL = server.URL
	require.NoError(t, svc.SetGatewayRoutingSettings(ctx, settings))
	r := packetLossRuntime(settings)
	svc.applyGatewayPacketLossProtection(ctx, settings, r, map[string]gatewayPacketLossSignal{"yt": {Fresh: true, Severe: true}, "peer": {Fresh: true, Recovered: true}}, now)
	svc = NewSettingService(repo, &config.Config{})
	svc.gatewayRoutingHTTPClient = server.Client()
	r, err := svc.GetGatewayRoutingRuntime(ctx)
	require.NoError(t, err)
	require.True(t, r.MonitorStale)
	require.Zero(t, r.Nodes[0].EffectiveWeight)
	require.Equal(t, "packet_loss", r.Nodes[0].AutoDisabledReason)
}
