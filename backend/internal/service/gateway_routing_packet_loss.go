package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"
)

const (
	defaultGatewayPacketLossCooldownMinutes = 5
	gatewayPacketLossStateKey               = "gateway_routing_packet_loss_state"
	gatewayICMPObserve                      = "observe"
	gatewayICMPLegacy                       = "legacy"
	gatewayOriginRecoveryStep               = 30 * time.Second
)

type gatewayPingTask struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	Interval int    `json:"interval"`
}

type gatewayPingRecord struct {
	TaskID int       `json:"task_id"`
	Time   time.Time `json:"time"`
	Value  *float64  `json:"value"`
	Client string    `json:"client"`
}

type gatewayPingResponse struct {
	Data struct {
		Tasks   []gatewayPingTask   `json:"tasks"`
		Records []gatewayPingRecord `json:"records"`
	} `json:"data"`
}

type gatewayPacketLossSignal struct {
	Fresh           bool
	Severe          bool
	Recovered       bool
	Percent         float64
	SampleAt        time.Time
	OriginUnhealthy bool
	OriginHealthy   bool
	OriginRecovered bool
	OriginSampleAt  time.Time
}

type gatewayPacketLossPause struct {
	Until         time.Time  `json:"until"`
	SampleAt      time.Time  `json:"sample_at"`
	OriginProbe   bool       `json:"origin_probe,omitempty"`
	RecoveryStage int        `json:"recovery_stage,omitempty"`
	RecoverySince *time.Time `json:"recovery_since,omitempty"`
}

func fetchGatewayPacketLossSignal(ctx context.Context, client *http.Client, baseURL, uuid string, now time.Time) gatewayPacketLossSignal {
	endpoint := baseURL + "/api/records/ping?hours=1&uuid=" + url.QueryEscape(uuid)
	var payload gatewayPingResponse
	if getGatewayRoutingMonitorJSON(ctx, client, endpoint, &payload) != nil {
		return gatewayPacketLossSignal{}
	}
	return evaluateGatewayPacketLoss(payload, uuid, now)
}

// Use independent ICMP targets, never the one-hour aggregate displayed by Komari.
// Every confirmation comes from a distinct, contiguous probe, not a runtime poll.
func evaluateGatewayPacketLoss(payload gatewayPingResponse, uuid string, now time.Time) gatewayPacketLossSignal {
	tasks := make(map[int]gatewayPingTask)
	for _, task := range payload.Data.Tasks {
		if task.Type == "icmp" && task.Interval >= 5 && task.Interval <= 300 {
			tasks[task.ID] = task
		}
	}
	if len(tasks) < 2 {
		return gatewayPacketLossSignal{}
	}
	byTask := make(map[int]map[int64]gatewayPingRecord)
	for _, record := range payload.Data.Records {
		if _, ok := tasks[record.TaskID]; !ok || record.Client != uuid || record.Value == nil || record.Time.IsZero() || record.Time.After(now.Add(15*time.Second)) {
			continue
		}
		if byTask[record.TaskID] == nil {
			byTask[record.TaskID] = make(map[int64]gatewayPingRecord)
		}
		byTask[record.TaskID][record.Time.UnixNano()] = record
	}
	signal := gatewayPacketLossSignal{}
	fresh, severe, recovered, total, lost := 0, 0, 0, 0, 0
	for id, task := range tasks {
		records := make([]gatewayPingRecord, 0, len(byTask[id]))
		for _, record := range byTask[id] {
			records = append(records, record)
		}
		sort.Slice(records, func(i, j int) bool { return records[i].Time.After(records[j].Time) })
		interval := time.Duration(task.Interval) * time.Second
		staleAfter := min(3*time.Minute, interval*5/2)
		if len(records) < 2 || now.Sub(records[0].Time) > staleAfter {
			continue
		}
		// Stop at a missing or implausibly close round instead of counting it as loss.
		n := min(6, len(records))
		for i := 1; i < n; i++ {
			gap := records[i-1].Time.Sub(records[i].Time)
			if gap < interval/2 || gap > interval*3/2 {
				n = i
				break
			}
		}
		if n < 2 {
			continue
		}
		records = records[:n]
		fresh++
		if signal.SampleAt.IsZero() || records[0].Time.Before(signal.SampleAt) {
			signal.SampleAt = records[0].Time
		}
		failures := func(from, to int) int {
			count := 0
			for _, record := range records[from:to] {
				if *record.Value < 0 {
					count++
				}
			}
			return count
		}
		window := min(5, n)
		total += window
		lost += failures(0, window)
		consecutive := failures(0, 2) == 2
		sustained := n == 6 && failures(0, 5) >= 2 && failures(1, 6) >= 2 && failures(0, 2) > 0
		if consecutive || sustained {
			severe++
		}
		if n >= 3 && failures(0, 3) == 0 {
			recovered++
		}
	}
	quorum := len(tasks)/2 + 1
	signal.Fresh = fresh >= quorum
	signal.Severe = severe >= quorum
	signal.Recovered = recovered >= quorum && severe == 0
	if total > 0 {
		signal.Percent = float64(lost) * 100 / float64(total)
	}
	return signal
}

// The settings table persists only cooldown deadlines, separately from the
// administrator's target weights. A control restart must not end a pause.
func (s *SettingService) applyGatewayPacketLossProtection(ctx context.Context, settings *GatewayRoutingSettings, runtime *GatewayRoutingRuntime, signals map[string]gatewayPacketLossSignal, now time.Time) {
	s.gatewayRoutingPacketLossMu.Lock()
	defer s.gatewayRoutingPacketLossMu.Unlock()
	previous := s.gatewayRoutingProtectedRuntime
	if previous == nil {
		previous = currentCachedGatewayRoutingRuntime(s)
	}
	defer func() { s.gatewayRoutingProtectedRuntime = cloneGatewayRoutingRuntime(runtime) }()
	legacy := settings.ICMPPauseMode == gatewayICMPLegacy
	for i, configured := range settings.Nodes {
		node := &runtime.Nodes[i]
		resetGatewayPacketLossRuntime(node, settings, configured)
		signal := signals[configured.ID]
		if !signal.OriginSampleAt.IsZero() {
			node.OriginSampleAt = &signal.OriginSampleAt
			ready := signal.OriginHealthy && !signal.OriginUnhealthy
			node.OriginReady = &ready
		}
		if signal.Fresh {
			node.ICMPProbeState = "observing"
			if signal.Severe {
				node.ICMPProbeState = "severe"
			} else if signal.Recovered {
				node.ICMPProbeState = "healthy"
			}
		}
	}
	base := cloneGatewayRoutingRuntime(runtime)
	if s.settingRepo == nil {
		preserveGatewayPacketLossRuntime(settings, runtime, previous)
		return
	}
	states := make(map[string]gatewayPacketLossPause)
	raw, err := s.settingRepo.GetValue(ctx, gatewayPacketLossStateKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		preserveGatewayPacketLossRuntime(settings, runtime, previous)
		return
	}
	if raw != "" && json.Unmarshal([]byte(raw), &states) != nil {
		preserveGatewayPacketLossRuntime(settings, runtime, previous)
		return
	}
	stateKey := func(node GatewayRoutingNodeSettings) string {
		return settings.MonitorURL + "|" + node.ID + "|" + node.Origin
	}
	next := make(map[string]gatewayPacketLossPause)
	// Require a proven healthy, routable alternative before newly isolating a
	// node. Shared probe failures must not remove the entire fleet at once.
	healthyPeer := false
	for i, configured := range settings.Nodes {
		signal := signals[configured.ID]
		pause, stored := states[stateKey(configured)]
		node := runtime.Nodes[i]
		if node.AutoDisabled || node.MonitorStale || signal.OriginUnhealthy {
			continue
		}
		if legacy && !(signal.Fresh && signal.Severe) && ((signal.Fresh && signal.Recovered) || signal.OriginHealthy) && !now.Before(pause.Until) {
			healthyPeer = true
		} else if !legacy && !stored && signal.OriginHealthy && !signal.OriginSampleAt.IsZero() && (node.EffectiveWeight > 0 || configured.ID == settings.OverflowNodeID) {
			healthyPeer = true
		}
	}
	for i, configured := range settings.Nodes {
		node := &runtime.Nodes[i]
		if !settings.PacketLossProtectionEnabled || !gatewayRoutingNodeCanReceiveRequests(settings, configured) {
			continue
		}
		signal := signals[configured.ID]
		node.PacketLossState = "unknown"
		if signal.Fresh {
			node.PacketLossPercent = &signal.Percent
			node.PacketLossSampleAt = &signal.SampleAt
			node.PacketLossState = "observing"
			if signal.Recovered {
				node.PacketLossState = "healthy"
			}
		}
		key := stateKey(configured)
		pause, paused := states[key]
		// OVH follows current readiness instead of retaining a timed origin
		// pause after a deployment restart. Explicit legacy ICMP pauses remain.
		if configured.ID == gatewayRoutingOVHNodeID && (!legacy || ((!paused || pause.OriginProbe) && !(signal.Fresh && signal.Severe))) {
			applyGatewayOVHOriginHealth(node)
			continue
		}
		if !legacy {
			if pause, retain := applyGatewayOriginHealth(node, settings, signal, pause, paused, healthyPeer, now); retain {
				next[key] = pause
			}
			continue
		}
		if !signal.OriginSampleAt.IsZero() && (!signal.Fresh || signal.OriginUnhealthy) {
			if signal.OriginUnhealthy {
				// An HTTP connectivity failure has no measured ICMP loss ratio.
				node.PacketLossPercent = nil
			}
			node.PacketLossSampleAt = &signal.OriginSampleAt
			node.PacketLossState = "observing"
			if signal.OriginRecovered {
				node.PacketLossState = "healthy"
			}
		}
		until := pause.Until
		recovered := signal.Fresh && signal.Recovered
		if pause.OriginProbe {
			recovered = signal.OriginRecovered && !(signal.Fresh && signal.Severe)
		}
		// A healthy probe cannot shorten the administrator-configured pause.
		if paused && recovered && !now.Before(until) {
			paused = false
		}
		if (signal.Fresh && signal.Severe) || signal.OriginUnhealthy {
			sampleAt := signal.SampleAt
			originProbe := signal.OriginUnhealthy && !(signal.Fresh && signal.Severe)
			if originProbe {
				sampleAt = signal.OriginSampleAt
			}
			if paused || (healthyPeer && !node.AutoDisabled && !node.MonitorStale) {
				if !paused || (!now.Before(until) && sampleAt.After(pause.SampleAt)) {
					until = now.Add(time.Duration(settings.PacketLossCooldownMinutes) * time.Minute)
					pause = gatewayPacketLossPause{Until: until, SampleAt: sampleAt, OriginProbe: originProbe}
				} else if !originProbe {
					pause.OriginProbe = false
				}
				paused = true
			} else {
				node.PacketLossState = "no_healthy_peer"
			}
		}
		if paused {
			reason := "packet_loss"
			node.ProtectionCause = "icmp_packet_loss"
			if pause.OriginProbe {
				reason = "origin_health"
				node.ProtectionCause = "origin_health"
			}
			next[key] = pause
			node.PacketLossPausedUntil = &until
			node.PacketLossState = "paused"
			if !now.Before(until) {
				node.PacketLossState = "awaiting_recovery"
			}
			if !node.AutoDisabled {
				applyAutoDisabledGatewayRoutingNode(node, reason)
			}
		}
	}
	encoded, _ := json.Marshal(next)
	if string(encoded) != raw && !(len(next) == 0 && raw == "") {
		if err := s.settingRepo.Set(ctx, gatewayPacketLossStateKey, string(encoded)); err != nil {
			// Do not advertise a new decision that would be lost on restart.
			*runtime = *base
			preserveGatewayPacketLossRuntime(settings, runtime, previous)
		}
	}
}

// OVH does not persist origin-failure cooldowns: a planned process restart
// may temporarily make readiness false, but fresh readiness restores its share.
// Independent traffic/monitor protection keeps its priority.
func applyGatewayOVHOriginHealth(node *GatewayRoutingNodeRuntime) {
	node.PacketLossPausedUntil = nil
	node.PacketLossState = "unknown"
	if node.OriginReady == nil {
		return
	}
	if *node.OriginReady {
		node.PacketLossState = "healthy"
		return
	}
	node.PacketLossState = "origin_unavailable"
	node.ProtectionCause = "origin_health"
	if !node.AutoDisabled {
		applyAutoDisabledGatewayRoutingNode(node, "origin_health")
		node.Status = "origin_unavailable"
	}
}

// In observe mode only origin evidence can change routing. This also covers
// old pauses without provenance: keep their deadline and require fresh readiness.
func applyGatewayOriginHealth(node *GatewayRoutingNodeRuntime, settings *GatewayRoutingSettings, signal gatewayPacketLossSignal, pause gatewayPacketLossPause, paused, healthyPeer bool, now time.Time) (gatewayPacketLossPause, bool) {
	node.PacketLossState = "unknown"
	if signal.OriginHealthy {
		node.PacketLossState = "healthy"
	}
	if signal.OriginUnhealthy {
		if paused || (!node.AutoDisabled && !node.MonitorStale && healthyPeer) {
			if !paused || pause.RecoveryStage > 0 || (!now.Before(pause.Until) && signal.OriginSampleAt.After(pause.SampleAt)) {
				pause = gatewayPacketLossPause{
					Until:       now.Add(time.Duration(settings.PacketLossCooldownMinutes) * time.Minute),
					SampleAt:    signal.OriginSampleAt,
					OriginProbe: true,
				}
			}
			paused = true
		} else {
			node.PacketLossState = "no_healthy_peer"
		}
	}
	if !paused {
		return pause, false
	}
	if !signal.OriginUnhealthy && signal.OriginRecovered && signal.OriginSampleAt.After(pause.SampleAt) && !now.Before(pause.Until) && !node.AutoDisabled && !node.MonitorStale {
		if pause.RecoveryStage == 0 || (pause.RecoverySince != nil && now.Sub(*pause.RecoverySince) >= gatewayOriginRecoveryStep && signal.OriginSampleAt.After(*pause.RecoverySince)) {
			pause.RecoveryStage++
			pause.RecoverySince = &now
		}
		configuredShare := node.TargetWeight
		if node.ID == gatewayRoutingOVHNodeID {
			configuredShare = settings.OVHGeoPercent
		}
		if pause.RecoveryStage > 3 || configuredShare == 0 {
			return pause, false
		}
	}
	reason := "origin_health"
	node.ProtectionCause = "origin_health"
	if !pause.OriginProbe {
		reason = "protection_unknown"
		node.ProtectionCause = "unknown"
	}
	node.PacketLossPausedUntil = &pause.Until
	if pause.RecoveryStage >= 1 && pause.RecoveryStage <= 3 && !node.AutoDisabled && !node.MonitorStale {
		percent := [...]int{5, 25, 50}[pause.RecoveryStage-1]
		node.EffectiveWeight = (node.EffectiveWeight*percent + 99) / 100
		node.PacketLossState = "recovering"
		return pause, true
	}
	node.PacketLossState = "paused"
	if !now.Before(pause.Until) {
		node.PacketLossState = "awaiting_recovery"
	}
	if !node.AutoDisabled {
		applyAutoDisabledGatewayRoutingNode(node, reason)
	}
	return pause, true
}

func resetGatewayPacketLossRuntime(node *GatewayRoutingNodeRuntime, settings *GatewayRoutingSettings, configured GatewayRoutingNodeSettings) {
	wasRecovering := node.PacketLossState == "recovering"
	node.PacketLossState, node.PacketLossPercent, node.PacketLossSampleAt, node.PacketLossPausedUntil = "", nil, nil, nil
	node.ICMPProbeState, node.OriginReady, node.OriginSampleAt = "", nil, nil
	node.ProtectionCause = ""
	// Independent quota/offline decisions keep their existing priority.
	if node.AutoDisabledReason == "packet_loss" || node.AutoDisabledReason == "origin_health" || node.AutoDisabledReason == "protection_unknown" || (wasRecovering && !node.AutoDisabled) {
		node.AutoDisabled, node.AutoDisabledReason = false, ""
		node.EffectiveWeight, node.Status = configured.TargetWeight, "active"
		if node.MonitorStale {
			node.Status = "monitor_stale"
		}
		if gatewayRoutingConfiguredShare(settings, configured) == 0 {
			node.Status = "manual_disabled"
		}
	}
}

func preserveGatewayPacketLossRuntime(settings *GatewayRoutingSettings, runtime, previous *GatewayRoutingRuntime) {
	for i, configured := range settings.Nodes {
		node := &runtime.Nodes[i]
		if !settings.PacketLossProtectionEnabled || !gatewayRoutingNodeCanReceiveRequests(settings, configured) {
			continue
		}
		if configured.ID == gatewayRoutingOVHNodeID && settings.ICMPPauseMode != gatewayICMPLegacy {
			applyGatewayOVHOriginHealth(node)
			continue
		}
		node.PacketLossState = "state_unavailable"
		if old := previousGatewayRoutingNode(previous, configured); old != nil && old.PacketLossPausedUntil != nil && settings.PacketLossProtectionEnabled {
			node.PacketLossPausedUntil = old.PacketLossPausedUntil
			node.ProtectionCause = old.ProtectionCause
			reason := old.AutoDisabledReason
			if reason == "" {
				switch old.ProtectionCause {
				case "origin_health":
					reason = "origin_health"
				case "icmp_packet_loss":
					reason = "packet_loss"
				default:
					reason = "protection_unknown"
				}
			}
			if !node.AutoDisabled {
				applyAutoDisabledGatewayRoutingNode(node, reason)
			}
		}
	}
}
