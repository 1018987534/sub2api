package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	defaultGatewayRoutingMonitorURL       = "https://check.nideyiyi.com"
	defaultGatewayRoutingThresholdPercent = 90
	gatewayRoutingRuntimeCacheTTL         = 30 * time.Second
	gatewayRoutingRuntimeErrorTTL         = 10 * time.Second
	gatewayRoutingMonitorTimeout          = 5 * time.Second
	gatewayRoutingMonitorStaleAfter       = 15 * time.Minute
	gatewayRoutingMonitorResponseLimit    = 4 << 20
	maxGatewayRoutingNodes                = 16
	maxGatewayRoutingWeight               = 10000
)

var gatewayRoutingNodeIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// GatewayRoutingSettings is the administrator-owned target configuration.
// Traffic protection changes only runtime effective weights and never rewrites it.
type GatewayRoutingSettings struct {
	MonitorURL               string                       `json:"monitor_url"`
	TrafficProtectionEnabled bool                         `json:"traffic_protection_enabled"`
	TrafficThresholdPercent  float64                      `json:"traffic_threshold_percent"`
	Nodes                    []GatewayRoutingNodeSettings `json:"nodes"`
}

type GatewayRoutingNodeSettings struct {
	ID           string `json:"id"`
	Origin       string `json:"origin"`
	TargetWeight int    `json:"target_weight"`
}

// GatewayRoutingRuntime is the read-only configuration consumed by the edge
// dispatcher and displayed in the administrator settings page.
type GatewayRoutingRuntime struct {
	GeneratedAt    time.Time                   `json:"generated_at"`
	MonitorChecked time.Time                   `json:"monitor_checked_at"`
	MonitorStale   bool                        `json:"monitor_stale"`
	MonitorError   string                      `json:"monitor_error,omitempty"`
	Nodes          []GatewayRoutingNodeRuntime `json:"nodes"`
}

type GatewayRoutingNodeRuntime struct {
	ID                  string     `json:"id"`
	Origin              string     `json:"origin"`
	TargetWeight        int        `json:"target_weight"`
	EffectiveWeight     int        `json:"effective_weight"`
	AutoDisabled        bool       `json:"auto_disabled"`
	Status              string     `json:"status"`
	TrafficLimitBytes   int64      `json:"traffic_limit_bytes"`
	TrafficUsedBytes    int64      `json:"traffic_used_bytes"`
	TrafficUsagePercent *float64   `json:"traffic_usage_percent"`
	TrafficLimitType    string     `json:"traffic_limit_type,omitempty"`
	Unlimited           bool       `json:"unlimited"`
	MonitorStale        bool       `json:"monitor_stale"`
	MonitorSampleAt     *time.Time `json:"monitor_sample_at,omitempty"`
}

type cachedGatewayRoutingRuntime struct {
	runtime   *GatewayRoutingRuntime
	expiresAt int64
}

type gatewayRoutingMonitorNode struct {
	UUID             string `json:"uuid"`
	Name             string `json:"name"`
	TrafficLimit     int64  `json:"traffic_limit"`
	TrafficLimitType string `json:"traffic_limit_type"`
}

type gatewayRoutingMonitorRecord struct {
	Time         time.Time `json:"time"`
	NetTotalUp   int64     `json:"net_total_up"`
	NetTotalDown int64     `json:"net_total_down"`
}

type gatewayRoutingNodesResponse struct {
	Data []gatewayRoutingMonitorNode `json:"data"`
}

type gatewayRoutingRecordsResponse struct {
	Data struct {
		Records []gatewayRoutingMonitorRecord `json:"records"`
	} `json:"data"`
}

func DefaultGatewayRoutingSettings() *GatewayRoutingSettings {
	return &GatewayRoutingSettings{
		MonitorURL:               defaultGatewayRoutingMonitorURL,
		TrafficProtectionEnabled: true,
		TrafficThresholdPercent:  defaultGatewayRoutingThresholdPercent,
		Nodes: []GatewayRoutingNodeSettings{
			{ID: "bwg-us-01", Origin: "https://control-origin.xiaohondou.com", TargetWeight: 5},
			{ID: "vmiss-us-01", Origin: "https://gateway-origin.xiaohondou.com", TargetWeight: 1},
			{ID: "yt-us-01", Origin: "https://gateway154-origin.xiaohondou.com", TargetWeight: 3},
			{ID: "vmiss-us-02", Origin: "https://gateway2-origin.xiaohondou.com", TargetWeight: 1},
		},
	}
}

func (s *SettingService) GetGatewayRoutingSettings(ctx context.Context) (*GatewayRoutingSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultGatewayRoutingSettings(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyGatewayRoutingSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultGatewayRoutingSettings(), nil
		}
		return nil, fmt.Errorf("get gateway routing settings: %w", err)
	}
	settings := DefaultGatewayRoutingSettings()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), settings); err != nil {
			return nil, fmt.Errorf("parse gateway routing settings: %w", err)
		}
	}
	if err := validateGatewayRoutingSettings(settings); err != nil {
		return nil, fmt.Errorf("stored gateway routing settings are invalid: %w", err)
	}
	return settings, nil
}

func (s *SettingService) SetGatewayRoutingSettings(ctx context.Context, settings *GatewayRoutingSettings) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("setting repository is unavailable")
	}
	if err := normalizeAndValidateGatewayRoutingSettings(settings); err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal gateway routing settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyGatewayRoutingSettings, string(raw)); err != nil {
		return fmt.Errorf("save gateway routing settings: %w", err)
	}
	s.gatewayRoutingRuntimeSF.Forget("gateway_routing_runtime")
	previous := currentCachedGatewayRoutingRuntime(s)
	s.gatewayRoutingRuntimeCache.Store(&cachedGatewayRoutingRuntime{runtime: previous, expiresAt: 0})
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}

func normalizeAndValidateGatewayRoutingSettings(settings *GatewayRoutingSettings) error {
	if settings == nil {
		return errors.New("gateway routing settings are required")
	}
	settings.MonitorURL = strings.TrimRight(strings.TrimSpace(settings.MonitorURL), "/")
	for i := range settings.Nodes {
		settings.Nodes[i].ID = strings.ToLower(strings.TrimSpace(settings.Nodes[i].ID))
		settings.Nodes[i].Origin = strings.TrimRight(strings.TrimSpace(settings.Nodes[i].Origin), "/")
	}
	return validateGatewayRoutingSettings(settings)
}

func validateGatewayRoutingSettings(settings *GatewayRoutingSettings) error {
	if settings == nil {
		return errors.New("gateway routing settings are required")
	}
	if err := validateHTTPSBaseURL(settings.MonitorURL, "monitor_url"); err != nil {
		return err
	}
	if settings.TrafficThresholdPercent < 1 || settings.TrafficThresholdPercent > 100 {
		return errors.New("traffic_threshold_percent must be between 1 and 100")
	}
	if len(settings.Nodes) == 0 || len(settings.Nodes) > maxGatewayRoutingNodes {
		return fmt.Errorf("nodes must contain between 1 and %d entries", maxGatewayRoutingNodes)
	}
	ids := make(map[string]struct{}, len(settings.Nodes))
	origins := make(map[string]struct{}, len(settings.Nodes))
	totalWeight := 0
	for i, node := range settings.Nodes {
		if !gatewayRoutingNodeIDPattern.MatchString(node.ID) {
			return fmt.Errorf("nodes[%d].id is invalid", i)
		}
		if _, exists := ids[node.ID]; exists {
			return fmt.Errorf("nodes[%d].id is duplicated", i)
		}
		ids[node.ID] = struct{}{}
		if err := validateHTTPSBaseURL(node.Origin, fmt.Sprintf("nodes[%d].origin", i)); err != nil {
			return err
		}
		if _, exists := origins[node.Origin]; exists {
			return fmt.Errorf("nodes[%d].origin is duplicated", i)
		}
		origins[node.Origin] = struct{}{}
		if node.TargetWeight < 0 || node.TargetWeight > maxGatewayRoutingWeight {
			return fmt.Errorf("nodes[%d].target_weight must be between 0 and %d", i, maxGatewayRoutingWeight)
		}
		totalWeight += node.TargetWeight
	}
	if totalWeight == 0 {
		return errors.New("at least one node target_weight must be greater than zero")
	}
	return nil
}

func validateHTTPSBaseURL(raw, field string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%s must be an https origin", field)
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must not include a path, query, or fragment", field)
	}
	return nil
}

func (s *SettingService) GetGatewayRoutingRuntime(ctx context.Context) (*GatewayRoutingRuntime, error) {
	settings, err := s.GetGatewayRoutingSettings(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if cached, ok := s.gatewayRoutingRuntimeCache.Load().(*cachedGatewayRoutingRuntime); ok && cached != nil && cached.runtime != nil {
		if now.UnixNano() < cached.expiresAt {
			return cloneGatewayRoutingRuntime(cached.runtime), nil
		}
	}

	value, err, _ := s.gatewayRoutingRuntimeSF.Do("gateway_routing_runtime", func() (any, error) {
		if cached, ok := s.gatewayRoutingRuntimeCache.Load().(*cachedGatewayRoutingRuntime); ok && cached != nil && cached.runtime != nil {
			if time.Now().UTC().UnixNano() < cached.expiresAt {
				return cloneGatewayRoutingRuntime(cached.runtime), nil
			}
		}

		previous := currentCachedGatewayRoutingRuntime(s)
		runtime, monitorErr := s.buildGatewayRoutingRuntime(context.WithoutCancel(ctx), settings, time.Now().UTC())
		cacheTTL := gatewayRoutingRuntimeCacheTTL
		if monitorErr != nil {
			cacheTTL = gatewayRoutingRuntimeErrorTTL
			runtime = staleGatewayRoutingRuntime(settings, previous, time.Now().UTC(), monitorErr.Error())
		}
		s.gatewayRoutingRuntimeCache.Store(&cachedGatewayRoutingRuntime{
			runtime:   cloneGatewayRoutingRuntime(runtime),
			expiresAt: time.Now().Add(cacheTTL).UnixNano(),
		})
		return runtime, nil
	})
	if err != nil {
		return nil, err
	}
	runtime, ok := value.(*GatewayRoutingRuntime)
	if !ok || runtime == nil {
		return nil, errors.New("gateway routing runtime is unavailable")
	}
	return cloneGatewayRoutingRuntime(runtime), nil
}

func currentCachedGatewayRoutingRuntime(s *SettingService) *GatewayRoutingRuntime {
	if s == nil {
		return nil
	}
	cached, _ := s.gatewayRoutingRuntimeCache.Load().(*cachedGatewayRoutingRuntime)
	if cached == nil || cached.runtime == nil {
		return nil
	}
	return cloneGatewayRoutingRuntime(cached.runtime)
}

func (s *SettingService) buildGatewayRoutingRuntime(ctx context.Context, settings *GatewayRoutingSettings, now time.Time) (*GatewayRoutingRuntime, error) {
	client := s.gatewayRoutingMonitorHTTPClient()
	monitorNodes, err := fetchGatewayRoutingMonitorNodes(ctx, client, settings.MonitorURL)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]gatewayRoutingMonitorNode, len(monitorNodes))
	for _, node := range monitorNodes {
		byName[node.Name] = node
	}

	previous := currentCachedGatewayRoutingRuntime(s)
	runtime := &GatewayRoutingRuntime{
		GeneratedAt:    now,
		MonitorChecked: now,
		Nodes:          make([]GatewayRoutingNodeRuntime, len(settings.Nodes)),
	}
	type matchedMonitorNode struct {
		index int
		node  gatewayRoutingMonitorNode
	}
	matched := make([]matchedMonitorNode, 0, len(settings.Nodes))
	for i, configured := range settings.Nodes {
		status := "active"
		if configured.TargetWeight == 0 {
			status = "manual_disabled"
		}
		runtime.Nodes[i] = GatewayRoutingNodeRuntime{
			ID:              configured.ID,
			Origin:          configured.Origin,
			TargetWeight:    configured.TargetWeight,
			EffectiveWeight: configured.TargetWeight,
			Status:          status,
		}
		monitorNode, ok := byName[configured.ID]
		if !ok {
			runtime.MonitorStale = true
			applyStaleGatewayRoutingNode(&runtime.Nodes[i], configured, previousGatewayRoutingNode(previous, configured))
			continue
		}
		runtime.Nodes[i].TrafficLimitBytes = monitorNode.TrafficLimit
		runtime.Nodes[i].TrafficLimitType = normalizeGatewayRoutingTrafficLimitType(monitorNode.TrafficLimitType)
		runtime.Nodes[i].Unlimited = monitorNode.TrafficLimit <= 0
		matched = append(matched, matchedMonitorNode{index: i, node: monitorNode})
	}

	group, groupCtx := errgroup.WithContext(ctx)
	for _, item := range matched {
		item := item
		group.Go(func() error {
			record, fetchErr := fetchGatewayRoutingLatestRecord(groupCtx, client, settings.MonitorURL, item.node.UUID)
			if fetchErr != nil {
				configured := settings.Nodes[item.index]
				applyStaleGatewayRoutingNode(&runtime.Nodes[item.index], configured, previousGatewayRoutingNode(previous, configured))
				return nil
			}
			runtime.Nodes[item.index].MonitorSampleAt = &record.Time
			if record.Time.IsZero() || record.Time.After(now.Add(5*time.Minute)) || now.Sub(record.Time) > gatewayRoutingMonitorStaleAfter {
				configured := settings.Nodes[item.index]
				applyStaleGatewayRoutingNode(&runtime.Nodes[item.index], configured, previousGatewayRoutingNode(previous, configured))
				return nil
			}
			used := gatewayRoutingTrafficUsed(runtime.Nodes[item.index].TrafficLimitType, record.NetTotalUp, record.NetTotalDown)
			runtime.Nodes[item.index].TrafficUsedBytes = used
			if runtime.Nodes[item.index].Unlimited {
				if runtime.Nodes[item.index].TargetWeight == 0 {
					return nil
				}
				runtime.Nodes[item.index].Status = "unlimited"
				return nil
			}
			percentage := float64(used) / float64(runtime.Nodes[item.index].TrafficLimitBytes) * 100
			runtime.Nodes[item.index].TrafficUsagePercent = &percentage
			if runtime.Nodes[item.index].TargetWeight > 0 && settings.TrafficProtectionEnabled && percentage >= settings.TrafficThresholdPercent {
				runtime.Nodes[item.index].EffectiveWeight = 0
				runtime.Nodes[item.index].AutoDisabled = true
				runtime.Nodes[item.index].Status = "auto_disabled"
			}
			return nil
		})
	}
	_ = group.Wait()
	for _, node := range runtime.Nodes {
		if node.MonitorStale {
			runtime.MonitorStale = true
			break
		}
	}
	return runtime, nil
}

func staleGatewayRoutingRuntime(settings *GatewayRoutingSettings, previous *GatewayRoutingRuntime, now time.Time, message string) *GatewayRoutingRuntime {
	runtime := &GatewayRoutingRuntime{
		GeneratedAt:    now,
		MonitorChecked: now,
		MonitorStale:   true,
		MonitorError:   message,
		Nodes:          make([]GatewayRoutingNodeRuntime, len(settings.Nodes)),
	}
	for i, node := range settings.Nodes {
		runtime.Nodes[i] = GatewayRoutingNodeRuntime{
			ID:              node.ID,
			Origin:          node.Origin,
			TargetWeight:    node.TargetWeight,
			EffectiveWeight: node.TargetWeight,
		}
		applyStaleGatewayRoutingNode(&runtime.Nodes[i], node, previousGatewayRoutingNode(previous, node))
	}
	return runtime
}

func previousGatewayRoutingNode(runtime *GatewayRoutingRuntime, configured GatewayRoutingNodeSettings) *GatewayRoutingNodeRuntime {
	if runtime == nil {
		return nil
	}
	for i := range runtime.Nodes {
		if runtime.Nodes[i].ID == configured.ID && runtime.Nodes[i].Origin == configured.Origin {
			return &runtime.Nodes[i]
		}
	}
	return nil
}

func applyStaleGatewayRoutingNode(runtime *GatewayRoutingNodeRuntime, configured GatewayRoutingNodeSettings, previous *GatewayRoutingNodeRuntime) {
	if runtime == nil {
		return
	}
	runtime.MonitorStale = true
	runtime.Status = "monitor_stale"
	runtime.EffectiveWeight = configured.TargetWeight
	if previous != nil {
		runtime.TrafficLimitBytes = previous.TrafficLimitBytes
		runtime.TrafficUsedBytes = previous.TrafficUsedBytes
		runtime.TrafficUsagePercent = previous.TrafficUsagePercent
		runtime.TrafficLimitType = previous.TrafficLimitType
		runtime.Unlimited = previous.Unlimited
		runtime.MonitorSampleAt = previous.MonitorSampleAt
		if previous.AutoDisabled {
			runtime.EffectiveWeight = 0
			runtime.AutoDisabled = true
			runtime.Status = "auto_disabled"
		}
	}
	if configured.TargetWeight == 0 {
		runtime.EffectiveWeight = 0
		runtime.AutoDisabled = false
		runtime.Status = "manual_disabled"
	}
}

func fetchGatewayRoutingMonitorNodes(ctx context.Context, client *http.Client, baseURL string) ([]gatewayRoutingMonitorNode, error) {
	var payload gatewayRoutingNodesResponse
	if err := getGatewayRoutingMonitorJSON(ctx, client, baseURL+"/api/nodes", &payload); err != nil {
		return nil, fmt.Errorf("fetch monitor nodes: %w", err)
	}
	if len(payload.Data) == 0 {
		return nil, errors.New("monitor returned no nodes")
	}
	return payload.Data, nil
}

func fetchGatewayRoutingLatestRecord(ctx context.Context, client *http.Client, baseURL, uuid string) (gatewayRoutingMonitorRecord, error) {
	endpoint, err := url.Parse(baseURL + "/api/records/load")
	if err != nil {
		return gatewayRoutingMonitorRecord{}, err
	}
	query := endpoint.Query()
	query.Set("uuid", uuid)
	query.Set("hours", "1")
	endpoint.RawQuery = query.Encode()
	var payload gatewayRoutingRecordsResponse
	if err := getGatewayRoutingMonitorJSON(ctx, client, endpoint.String(), &payload); err != nil {
		return gatewayRoutingMonitorRecord{}, err
	}
	if len(payload.Data.Records) == 0 {
		return gatewayRoutingMonitorRecord{}, errors.New("monitor returned no records")
	}
	sort.Slice(payload.Data.Records, func(i, j int) bool {
		return payload.Data.Records[i].Time.Before(payload.Data.Records[j].Time)
	})
	return payload.Data.Records[len(payload.Data.Records)-1], nil
}

func getGatewayRoutingMonitorJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	requestCtx, cancel := context.WithTimeout(ctx, gatewayRoutingMonitorTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected monitor status %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, gatewayRoutingMonitorResponseLimit))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode monitor response: %w", err)
	}
	return nil
}

func (s *SettingService) gatewayRoutingMonitorHTTPClient() *http.Client {
	if s != nil && s.gatewayRoutingHTTPClient != nil {
		return s.gatewayRoutingHTTPClient
	}
	return http.DefaultClient
}

func normalizeGatewayRoutingTrafficLimitType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "up", "down", "sum", "min", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "max"
	}
}

func gatewayRoutingTrafficUsed(limitType string, up, down int64) int64 {
	if up < 0 {
		up = 0
	}
	if down < 0 {
		down = 0
	}
	switch normalizeGatewayRoutingTrafficLimitType(limitType) {
	case "up":
		return up
	case "down":
		return down
	case "sum":
		return up + down
	case "min":
		if up < down {
			return up
		}
		return down
	default:
		if up > down {
			return up
		}
		return down
	}
}

func cloneGatewayRoutingRuntime(source *GatewayRoutingRuntime) *GatewayRoutingRuntime {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Nodes = append([]GatewayRoutingNodeRuntime(nil), source.Nodes...)
	return &clone
}
