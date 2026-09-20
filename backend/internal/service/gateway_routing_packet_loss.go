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
	Fresh     bool
	Severe    bool
	Recovered bool
	Percent   float64
	SampleAt  time.Time
}

type gatewayPacketLossPause struct {
	Until    time.Time `json:"until"`
	SampleAt time.Time `json:"sample_at"`
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
	for i, configured := range settings.Nodes {
		resetGatewayPacketLossRuntime(&runtime.Nodes[i], configured)
	}
	base := cloneGatewayRoutingRuntime(runtime)
	if s.settingRepo == nil {
		preserveGatewayPacketLossRuntime(settings, runtime, currentCachedGatewayRoutingRuntime(s))
		return
	}
	states := make(map[string]gatewayPacketLossPause)
	raw, err := s.settingRepo.GetValue(ctx, gatewayPacketLossStateKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		preserveGatewayPacketLossRuntime(settings, runtime, currentCachedGatewayRoutingRuntime(s))
		return
	}
	if raw != "" && json.Unmarshal([]byte(raw), &states) != nil {
		preserveGatewayPacketLossRuntime(settings, runtime, currentCachedGatewayRoutingRuntime(s))
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
		until := states[stateKey(configured)].Until
		if runtime.Nodes[i].EffectiveWeight > 0 && !runtime.Nodes[i].MonitorStale && signal.Fresh && signal.Recovered && !now.Before(until) {
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
		until := pause.Until
		if paused && !now.Before(until) && signal.Fresh && signal.Recovered {
			paused = false
		}
		if signal.Fresh && signal.Severe {
			if paused || (healthyPeer && !node.AutoDisabled && !node.MonitorStale) {
				if !paused || (!now.Before(until) && signal.SampleAt.After(pause.SampleAt)) {
					until = now.Add(time.Duration(settings.PacketLossCooldownMinutes) * time.Minute)
					pause = gatewayPacketLossPause{Until: until, SampleAt: signal.SampleAt}
				}
				paused = true
			} else {
				node.PacketLossState = "no_healthy_peer"
			}
		}
		if paused {
			next[key] = pause
			node.PacketLossPausedUntil = &until
			node.PacketLossState = "paused"
			if !now.Before(until) {
				node.PacketLossState = "awaiting_recovery"
			}
			if !node.AutoDisabled {
				applyAutoDisabledGatewayRoutingNode(node, "packet_loss")
			}
		}
	}
	encoded, _ := json.Marshal(next)
	if string(encoded) != raw && !(len(next) == 0 && raw == "") {
		if err := s.settingRepo.Set(ctx, gatewayPacketLossStateKey, string(encoded)); err != nil {
			// Do not advertise a new decision that would be lost on restart.
			*runtime = *base
			preserveGatewayPacketLossRuntime(settings, runtime, currentCachedGatewayRoutingRuntime(s))
		}
	}
}

func resetGatewayPacketLossRuntime(node *GatewayRoutingNodeRuntime, configured GatewayRoutingNodeSettings) {
	node.PacketLossState, node.PacketLossPercent, node.PacketLossSampleAt, node.PacketLossPausedUntil = "", nil, nil, nil
	// Independent quota/offline decisions keep their existing priority.
	if node.AutoDisabledReason == "packet_loss" {
		node.AutoDisabled, node.AutoDisabledReason = false, ""
		node.EffectiveWeight, node.Status = configured.TargetWeight, "active"
		if node.MonitorStale {
			node.Status = "monitor_stale"
		}
		if configured.TargetWeight == 0 {
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
		node.PacketLossState = "state_unavailable"
		if old := previousGatewayRoutingNode(previous, configured); old != nil && old.PacketLossPausedUntil != nil && settings.PacketLossProtectionEnabled {
			node.PacketLossPausedUntil = old.PacketLossPausedUntil
			if !node.AutoDisabled {
				applyAutoDisabledGatewayRoutingNode(node, "packet_loss")
			}
		}
	}
}
