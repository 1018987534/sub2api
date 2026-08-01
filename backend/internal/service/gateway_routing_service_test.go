package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type gatewayRoutingRepoStub struct {
	mu     sync.Mutex
	values map[string]string
}

func newGatewayRoutingRepoStub() *gatewayRoutingRepoStub {
	return &gatewayRoutingRepoStub{values: make(map[string]string)}
}

func (r *gatewayRoutingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (r *gatewayRoutingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *gatewayRoutingRepoStub) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *gatewayRoutingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r *gatewayRoutingRepoStub) SetMultiple(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		if err := r.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *gatewayRoutingRepoStub) GetAll(context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]string, len(r.values))
	for key, value := range r.values {
		values[key] = value
	}
	return values, nil
}

func (r *gatewayRoutingRepoStub) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func TestGatewayRoutingSettingsDefaultsAndValidation(t *testing.T) {
	repo := newGatewayRoutingRepoStub()
	service := NewSettingService(repo, &config.Config{})

	settings, err := service.GetGatewayRoutingSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{50, 10, 30, 10}, []int{
		settings.Nodes[0].TargetWeight,
		settings.Nodes[1].TargetWeight,
		settings.Nodes[2].TargetWeight,
		settings.Nodes[3].TargetWeight,
	})

	settings.Nodes[0].TargetWeight = 0
	settings.Nodes[1].TargetWeight = 0
	settings.Nodes[2].TargetWeight = 0
	settings.Nodes[3].TargetWeight = 0
	require.ErrorContains(t, service.SetGatewayRoutingSettings(context.Background(), settings), "at least one node")

	settings = DefaultGatewayRoutingSettings()
	settings.Nodes[1].ID = settings.Nodes[0].ID
	require.ErrorContains(t, service.SetGatewayRoutingSettings(context.Background(), settings), "duplicated")

	settings = DefaultGatewayRoutingSettings()
	settings.Nodes[0].TargetWeight--
	require.ErrorContains(t, service.SetGatewayRoutingSettings(context.Background(), settings), "must total 100%")
}

func TestGatewayRoutingSettingsMigratesLegacyRatiosOnRead(t *testing.T) {
	repo := newGatewayRoutingRepoStub()
	repo.values[SettingKeyGatewayRoutingSettings] = `{"monitor_url":"https://check.example","traffic_protection_enabled":true,"traffic_threshold_percent":90,"nodes":[{"id":"control","origin":"https://control.example","target_weight":5},{"id":"gateway","origin":"https://gateway.example","target_weight":1},{"id":"gateway-154","origin":"https://gateway-154.example","target_weight":3},{"id":"gateway-2","origin":"https://gateway-2.example","target_weight":1}]}`
	service := NewSettingService(repo, &config.Config{})

	settings, err := service.GetGatewayRoutingSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{50, 10, 30, 10}, []int{
		settings.Nodes[0].TargetWeight,
		settings.Nodes[1].TargetWeight,
		settings.Nodes[2].TargetWeight,
		settings.Nodes[3].TargetWeight,
	})
}

func TestGatewayRoutingRuntimeAutoDisablesAtThresholdAndKeepsUnlimitedNode(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/nodes":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"uuid": "limited-uuid", "name": "limited", "traffic_limit": int64(1000), "traffic_limit_type": "sum"},
				{"uuid": "unlimited-uuid", "name": "unlimited", "traffic_limit": int64(0), "traffic_limit_type": "max"},
			}})
		case "/api/records/load":
			uuid := r.URL.Query().Get("uuid")
			record := map[string]any{"time": now, "net_total_up": int64(100), "net_total_down": int64(100)}
			if uuid == "limited-uuid" {
				record["net_total_up"] = int64(500)
				record["net_total_down"] = int64(450)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"records": []map[string]any{record}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	repo := newGatewayRoutingRepoStub()
	service := NewSettingService(repo, &config.Config{})
	service.gatewayRoutingHTTPClient = server.Client()
	settings := &GatewayRoutingSettings{
		MonitorURL:               server.URL,
		TrafficProtectionEnabled: true,
		TrafficThresholdPercent:  90,
		Nodes: []GatewayRoutingNodeSettings{
			{ID: "limited", Origin: "https://limited.example", TargetWeight: 50},
			{ID: "unlimited", Origin: "https://unlimited.example", TargetWeight: 50},
		},
	}
	require.NoError(t, service.SetGatewayRoutingSettings(context.Background(), settings))

	runtime, err := service.GetGatewayRoutingRuntime(context.Background())
	require.NoError(t, err)
	require.False(t, runtime.MonitorStale)
	require.Equal(t, 0, runtime.Nodes[0].EffectiveWeight)
	require.True(t, runtime.Nodes[0].AutoDisabled)
	require.Equal(t, "auto_disabled", runtime.Nodes[0].Status)
	require.InDelta(t, 95, *runtime.Nodes[0].TrafficUsagePercent, 0.001)
	require.Equal(t, 50, runtime.Nodes[1].EffectiveWeight)
	require.True(t, runtime.Nodes[1].Unlimited)
	require.Equal(t, "unlimited", runtime.Nodes[1].Status)
}

func TestGatewayRoutingRuntimeKeepsLastGoodResultWhenMonitorFails(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var fail atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/nodes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"uuid": "node-uuid", "name": "node-1", "traffic_limit": int64(1000), "traffic_limit_type": "sum"},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"records": []map[string]any{
			{"time": now, "net_total_up": int64(100), "net_total_down": int64(100)},
		}}})
	}))
	defer server.Close()

	repo := newGatewayRoutingRepoStub()
	service := NewSettingService(repo, &config.Config{})
	service.gatewayRoutingHTTPClient = server.Client()
	settings := &GatewayRoutingSettings{
		MonitorURL:               server.URL,
		TrafficProtectionEnabled: true,
		TrafficThresholdPercent:  90,
		Nodes: []GatewayRoutingNodeSettings{
			{ID: "node-1", Origin: "https://node-1.example", TargetWeight: 100},
		},
	}
	require.NoError(t, service.SetGatewayRoutingSettings(context.Background(), settings))
	first, err := service.GetGatewayRoutingRuntime(context.Background())
	require.NoError(t, err)
	require.False(t, first.MonitorStale)

	fail.Store(true)
	service.gatewayRoutingRuntimeCache.Store(&cachedGatewayRoutingRuntime{runtime: first, expiresAt: 0})
	second, err := service.GetGatewayRoutingRuntime(context.Background())
	require.NoError(t, err)
	require.True(t, second.MonitorStale)
	require.Equal(t, first.Nodes[0].EffectiveWeight, second.Nodes[0].EffectiveWeight)
	require.NotEmpty(t, second.MonitorError)
}

func TestGatewayRoutingRuntimeKeepsAutoDisabledNodeOutWhenRecordTurnsStale(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var failRecords atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/nodes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"uuid": "node-uuid", "name": "node-1", "traffic_limit": int64(1000), "traffic_limit_type": "sum"},
			}})
			return
		}
		if failRecords.Load() {
			http.Error(w, "temporary record failure", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"records": []map[string]any{
			{"time": now, "net_total_up": int64(500), "net_total_down": int64(450)},
		}}})
	}))
	defer server.Close()

	repo := newGatewayRoutingRepoStub()
	service := NewSettingService(repo, &config.Config{})
	service.gatewayRoutingHTTPClient = server.Client()
	settings := &GatewayRoutingSettings{
		MonitorURL:               server.URL,
		TrafficProtectionEnabled: true,
		TrafficThresholdPercent:  90,
		Nodes: []GatewayRoutingNodeSettings{
			{ID: "node-1", Origin: "https://node-1.example", TargetWeight: 100},
		},
	}
	require.NoError(t, service.SetGatewayRoutingSettings(context.Background(), settings))
	first, err := service.GetGatewayRoutingRuntime(context.Background())
	require.NoError(t, err)
	require.True(t, first.Nodes[0].AutoDisabled)
	require.Equal(t, 0, first.Nodes[0].EffectiveWeight)

	failRecords.Store(true)
	settings.Nodes[0].TargetWeight = 100
	require.NoError(t, service.SetGatewayRoutingSettings(context.Background(), settings))
	second, err := service.GetGatewayRoutingRuntime(context.Background())
	require.NoError(t, err)
	require.True(t, second.MonitorStale)
	require.True(t, second.Nodes[0].MonitorStale)
	require.True(t, second.Nodes[0].AutoDisabled)
	require.Equal(t, 100, second.Nodes[0].TargetWeight)
	require.Equal(t, 0, second.Nodes[0].EffectiveWeight)
	require.Equal(t, "auto_disabled", second.Nodes[0].Status)
}
