package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func writeGatewayReadyResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ready","checks":{"database":"ok","redis":"ok"}}`))
}

func TestGatewayOriginProbeRequiresSharedStateReady(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ready  bool
	}{
		{"ready", http.StatusOK, `{"status":"ready","checks":{"database":"ok","redis":"ok"}}`, true},
		{"nginx access denied", http.StatusForbidden, `<html>Forbidden</html>`, false},
		{"empty success", http.StatusOK, "", false},
		{"redis unavailable", http.StatusOK, `{"status":"ready","checks":{"database":"ok","redis":"unavailable"}}`, false},
		{"database unavailable", http.StatusServiceUnavailable, `{"status":"not_ready","checks":{"database":"unavailable","redis":"ok"}}`, false},
		{"not ready", http.StatusOK, `{"status":"not_ready","checks":{"database":"ok","redis":"ok"}}`, false},
		{"invalid JSON", http.StatusOK, `<html>OK</html>`, false},
		{"redirect", http.StatusFound, `{"status":"ready","checks":{"database":"ok","redis":"ok"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			require.Equal(t, tc.ready, probeGatewayOrigin(server.Client(), server.URL))
		})
	}
}

func TestGatewayOriginProbeConfirmsOnlyFailures(t *testing.T) {
	for _, scenario := range []string{"healthy", "access denied", "transient", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/health/ready", r.URL.Path)
				require.Equal(t, "no-cache", r.Header.Get("Cache-Control"))
				n := calls.Add(1)
				if scenario == "access denied" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if scenario == "failed" || (scenario == "transient" && n == 1) {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				writeGatewayReadyResponse(w)
			}))
			defer server.Close()
			svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
			settings := packetLossSettings()
			settings.Nodes = []GatewayRoutingNodeSettings{{ID: "standby", Origin: server.URL, TargetWeight: 0}}
			svc.refreshGatewayOriginProbes(settings, "fixture", nil)
			probe := svc.gatewayRoutingOriginProbeCache.nodes["standby"]
			require.Equal(t, scenario == "failed" || scenario == "access denied", probe.Failed)
			if scenario == "healthy" {
				require.EqualValues(t, 1, calls.Load())
			} else {
				require.EqualValues(t, 2, calls.Load())
			}
		})
	}
}

func TestGatewayOriginProbeDoesNotBlockOrMultiplyCustomerPolls(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		writeGatewayReadyResponse(w)
	}))
	defer server.Close()
	svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
	settings := packetLossSettings()
	settings.Nodes = []GatewayRoutingNodeSettings{{ID: "yt", Origin: server.URL, TargetWeight: 100}}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			svc.gatewayRoutingProbeSignals(settings, nil, time.Now())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("customer poll waited for a background health probe")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("probe was not started")
	}
	close(release)
	require.Eventually(t, func() bool {
		svc.gatewayRoutingOriginProbeMu.Lock()
		defer svc.gatewayRoutingOriginProbeMu.Unlock()
		return !svc.gatewayRoutingOriginProbeInFlight
	}, time.Second, time.Millisecond)
	require.EqualValues(t, 1, calls.Load(), "100 customer polls share one small probe")
	for i := 0; i < 100; i++ {
		signals := svc.gatewayRoutingProbeSignals(settings, nil, time.Now())
		require.True(t, signals["yt"].OriginHealthy)
		require.False(t, signals["yt"].OriginRecovered, "repeated reads are not successful probe rounds")
	}
	require.EqualValues(t, 1, calls.Load())
}

func TestGatewayOriginProbeRecoveryFreshnessAndSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeGatewayReadyResponse(w)
	}))
	defer server.Close()
	svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
	settings := packetLossSettings()
	settings.Nodes = []GatewayRoutingNodeSettings{{ID: "yt", Origin: server.URL, TargetWeight: 100}}
	key := settings.MonitorURL + "|yt|" + server.URL
	for i := 1; i <= 3; i++ {
		svc.refreshGatewayOriginProbes(settings, key, svc.gatewayRoutingOriginProbeCache)
		signals := svc.gatewayRoutingProbeSignals(settings, nil, time.Now())
		require.Equal(t, i == 3, signals["yt"].OriginRecovered)
	}
	signals := svc.gatewayRoutingProbeSignals(settings, nil, time.Now().Add(gatewayOriginProbeStaleAfter+time.Second))
	require.False(t, signals["yt"].OriginHealthy, "expired samples do not establish a healthy peer")
	require.False(t, signals["yt"].OriginRecovered)
	settings.PacketLossProtectionEnabled = false
	require.Empty(t, svc.gatewayRoutingProbeSignals(settings, nil, time.Now()))
	// A changed origin cannot inherit the previous origin's recovery streak.
	changed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeGatewayReadyResponse(w)
	}))
	defer changed.Close()
	settings.Nodes[0].Origin = changed.URL
	settings.PacketLossProtectionEnabled = true
	signals = svc.gatewayRoutingProbeSignals(settings, nil, time.Now())
	require.False(t, signals["yt"].OriginHealthy)
	// Wait for the bounded background flight before the test exits.
	require.Eventually(t, func() bool {
		svc.gatewayRoutingOriginProbeMu.Lock()
		defer svc.gatewayRoutingOriginProbeMu.Unlock()
		return !svc.gatewayRoutingOriginProbeInFlight
	}, 3*time.Second, 5*time.Millisecond)
}

func TestGatewayOriginProbePauseThenAutomaticallyRecovers(t *testing.T) {
	var ready atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeGatewayReadyResponse(w)
	}))
	defer server.Close()
	repo := newGatewayRoutingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	settings := packetLossSettings()
	settings.PacketLossCooldownMinutes = 60
	settings.Nodes[0].Origin = server.URL
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeGatewayReadyResponse(w)
	}))
	defer peer.Close()
	settings.Nodes[1].Origin = peer.URL
	key := settings.MonitorURL
	for _, node := range settings.Nodes {
		key += "|" + node.ID + "|" + node.Origin
	}
	apply := func() *GatewayRoutingRuntime {
		now := time.Now().UTC()
		icmp := map[string]gatewayPacketLossSignal{
			"yt":   {Fresh: true, Recovered: true, SampleAt: now},
			"peer": {Fresh: true, Recovered: true, SampleAt: now},
		}
		runtime := packetLossRuntime(settings)
		svc.applyGatewayPacketLossProtection(context.Background(), settings, runtime,
			svc.gatewayRoutingProbeSignals(settings, icmp, now), now)
		return runtime
	}
	svc.refreshGatewayOriginProbes(settings, key, nil)
	runtime := apply()
	require.Zero(t, runtime.Nodes[0].EffectiveWeight)
	deadline := *runtime.Nodes[0].PacketLossPausedUntil
	ready.Store(true)
	for round := 1; round <= 3; round++ {
		svc.refreshGatewayOriginProbes(settings, key, svc.gatewayRoutingOriginProbeCache)
		runtime = apply()
		if round < 3 {
			require.Zero(t, runtime.Nodes[0].EffectiveWeight)
			require.NotNil(t, runtime.Nodes[0].PacketLossPausedUntil)
		} else {
			require.Equal(t, 70, runtime.Nodes[0].EffectiveWeight)
			require.Nil(t, runtime.Nodes[0].PacketLossPausedUntil)
			require.Equal(t, "{}", repo.values[gatewayPacketLossStateKey])
		}
	}
	require.True(t, time.Now().Before(deadline), "three actual readiness rounds recover before the one-hour deadline")
}

func TestGatewayOriginProbeTimeoutIsBoundedAndConfirmed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
	settings := packetLossSettings()
	settings.Nodes = []GatewayRoutingNodeSettings{{ID: "yt", Origin: server.URL, TargetWeight: 100}}
	start := time.Now()
	svc.refreshGatewayOriginProbes(settings, "fixture", nil)
	require.Less(t, time.Since(start), 3*time.Second)
	require.EqualValues(t, 2, calls.Load())
	require.True(t, svc.gatewayRoutingOriginProbeCache.nodes["yt"].Failed)
}
