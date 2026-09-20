package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// This wire-contract regression also compiles against the pre-protection code.
func TestGatewayRoutingPacketLossContract(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/nodes":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"uuid": "yt-uuid", "name": "yt", "traffic_limit": 0}, {"uuid": "peer-uuid", "name": "peer", "traffic_limit": 0}}})
		case "/api/records/load":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"records": []map[string]any{{"time": now}}}})
		case "/api/records/ping":
			tasks := []map[string]any{}
			records := []map[string]any{}
			for task := 1; task <= 3; task++ {
				tasks = append(tasks, map[string]any{"id": task, "type": "icmp", "interval": 60})
				for sample := 0; sample < 6; sample++ {
					value := 160
					if r.URL.Query().Get("uuid") == "yt-uuid" && sample < 2 {
						value = -1
					}
					records = append(records, map[string]any{"client": r.URL.Query().Get("uuid"), "task_id": task, "time": now.Add(-time.Duration(sample) * time.Minute), "value": value})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"tasks": tasks, "records": records}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var settings GatewayRoutingSettings
	require.NoError(t, json.Unmarshal([]byte(`{"packet_loss_protection_enabled":true,"packet_loss_cooldown_minutes":7,"nodes":[{"id":"yt","origin":"https://yt.example","target_weight":70},{"id":"peer","origin":"https://peer.example","target_weight":30}]}`), &settings))
	settings.MonitorURL = server.URL
	svc := NewSettingService(newGatewayRoutingRepoStub(), &config.Config{})
	svc.gatewayRoutingHTTPClient = server.Client()
	require.NoError(t, svc.SetGatewayRoutingSettings(context.Background(), &settings))
	runtime, err := svc.GetGatewayRoutingRuntime(context.Background())
	require.NoError(t, err)
	t.Logf("YT target=70 effective=%d status=%s; healthy peer effective=%d", runtime.Nodes[0].EffectiveWeight, runtime.Nodes[0].Status, runtime.Nodes[1].EffectiveWeight)
	require.Zero(t, runtime.Nodes[0].EffectiveWeight)
	require.Equal(t, "packet_loss", runtime.Nodes[0].AutoDisabledReason)
	require.Equal(t, 70, runtime.Nodes[0].TargetWeight)
	require.Equal(t, 30, runtime.Nodes[1].EffectiveWeight)
}
