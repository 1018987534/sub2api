package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	gatewayOriginProbeInterval   = 2 * time.Second
	gatewayOriginProbeTimeout    = time.Second
	gatewayOriginProbeStaleAfter = 10 * time.Second
)

type gatewayOriginProbe struct {
	SampleAt  time.Time
	Successes int
	Failed    bool
}

type gatewayOriginProbeSnapshot struct {
	key       string
	expiresAt time.Time
	nodes     map[string]gatewayOriginProbe
}

// Connectivity probes run outside customer requests and never call a model endpoint.
// A single flight covers the entire fleet, including zero-weight standbys.
func (s *SettingService) gatewayRoutingProbeSignals(settings *GatewayRoutingSettings, icmp map[string]gatewayPacketLossSignal, now time.Time) map[string]gatewayPacketLossSignal {
	signals := make(map[string]gatewayPacketLossSignal, len(settings.Nodes))
	for id, signal := range icmp {
		signals[id] = signal
	}
	if !settings.PacketLossProtectionEnabled {
		return signals
	}
	var key strings.Builder
	key.WriteString(settings.MonitorURL)
	for _, node := range settings.Nodes {
		key.WriteString("|" + node.ID + "|" + node.Origin)
	}
	s.gatewayRoutingOriginProbeMu.Lock()
	previous := s.gatewayRoutingOriginProbeCache
	if previous != nil && previous.key != key.String() {
		previous = nil
	}
	if previous != nil {
		for id, probe := range previous.nodes {
			if now.Sub(probe.SampleAt) > gatewayOriginProbeStaleAfter {
				continue
			}
			signal := signals[id]
			signal.OriginSampleAt = probe.SampleAt
			signal.OriginUnhealthy = probe.Failed
			signal.OriginHealthy = probe.Successes > 0
			signal.OriginRecovered = probe.Successes >= 3
			signals[id] = signal
		}
	}
	if !s.gatewayRoutingOriginProbeInFlight && (previous == nil || !now.Before(previous.expiresAt)) {
		s.gatewayRoutingOriginProbeInFlight = true
		go s.refreshGatewayOriginProbes(cloneGatewayRoutingSettings(settings), key.String(), previous)
	}
	s.gatewayRoutingOriginProbeMu.Unlock()
	return signals
}

func (s *SettingService) refreshGatewayOriginProbes(settings *GatewayRoutingSettings, key string, previous *gatewayOriginProbeSnapshot) {
	client := s.gatewayRoutingHTTPClient
	if client == nil {
		client = &http.Client{
			Timeout:       gatewayOriginProbeTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	probes := make([]gatewayOriginProbe, len(settings.Nodes))
	var group sync.WaitGroup
	for i, node := range settings.Nodes {
		group.Add(1)
		go func() {
			defer group.Done()
			healthy := probeGatewayOrigin(client, node.Origin)
			probe := gatewayOriginProbe{}
			if healthy {
				probe.Successes = 1
				if previous != nil {
					old := previous.nodes[node.ID]
					if time.Since(old.SampleAt) <= gatewayOriginProbeStaleAfter {
						probe.Successes = min(3, old.Successes+1)
					}
				}
			} else if probeGatewayOrigin(client, node.Origin) {
				// One transient failure must not remove an otherwise usable node.
				probe.Successes = 1
			} else {
				probe.Failed = true
			}
			probe.SampleAt = time.Now().UTC()
			probes[i] = probe
		}()
	}
	group.Wait()
	next := &gatewayOriginProbeSnapshot{key: key, expiresAt: time.Now().Add(gatewayOriginProbeInterval), nodes: make(map[string]gatewayOriginProbe, len(probes))}
	for i, node := range settings.Nodes {
		next.nodes[node.ID] = probes[i]
	}
	s.gatewayRoutingOriginProbeMu.Lock()
	s.gatewayRoutingOriginProbeCache = next
	s.gatewayRoutingOriginProbeInFlight = false
	s.gatewayRoutingOriginProbeMu.Unlock()
}

func probeGatewayOrigin(client *http.Client, origin string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gatewayOriginProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/health/ready", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	// Production origins intentionally reject non-Cloudflare callers at Nginx.
	// A prompt 4xx still proves TCP/TLS/HTTP connectivity; it is not packet loss.
	// Keep transport failures, timeouts and 5xx responses eligible for confirmation.
	return err == nil && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusInternalServerError
}
