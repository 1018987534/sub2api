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
	gatewayRoutingAdmissionCacheTTL       = 5 * time.Second
	gatewayRoutingMonitorTimeout          = 5 * time.Second
	gatewayRoutingMonitorStaleAfter       = 15 * time.Minute
	gatewayRoutingHealthStaleAfter        = 3 * time.Minute
	gatewayRoutingMonitorResponseLimit    = 4 << 20
	maxGatewayRoutingNodes                = 16
	maxGatewayRoutingWeight               = 100
	maxGatewayRoutingConcurrency          = 100000
)

var gatewayRoutingNodeIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// GatewayRoutingSettings is the administrator-owned target percentage configuration.
// Traffic protection changes only runtime effective weights and never rewrites it.
type GatewayRoutingSettings struct {
	MonitorURL               string                       `json:"monitor_url"`
	TrafficProtectionEnabled bool                         `json:"traffic_protection_enabled"`
	HealthProtectionEnabled  bool                         `json:"health_protection_enabled"`
	TrafficThresholdPercent  float64                      `json:"traffic_threshold_percent"`
	OverflowNodeID           string                       `json:"overflow_node_id"`
	Nodes                    []GatewayRoutingNodeSettings `json:"nodes"`
}

func (s *GatewayRoutingSettings) UnmarshalJSON(data []byte) error {
	type gatewayRoutingSettingsJSON GatewayRoutingSettings
	defaults := DefaultGatewayRoutingSettings()
	*s = *defaults
	return json.Unmarshal(data, (*gatewayRoutingSettingsJSON)(s))
}

type GatewayRoutingNodeSettings struct {
	ID             string `json:"id"`
	Origin         string `json:"origin"`
	TargetWeight   int    `json:"target_weight"`
	MaxConcurrency int    `json:"max_concurrency"`
}

// GatewayRoutingRuntime is the read-only configuration consumed by the edge
// dispatcher and displayed in the administrator settings page.
type GatewayRoutingRuntime struct {
	GeneratedAt    time.Time                   `json:"generated_at"`
	MonitorChecked time.Time                   `json:"monitor_checked_at"`
	MonitorStale   bool                        `json:"monitor_stale"`
	MonitorError   string                      `json:"monitor_error,omitempty"`
	CapacityError  string                      `json:"capacity_error,omitempty"`
	OverflowNodeID string                      `json:"overflow_node_id"`
	Nodes          []GatewayRoutingNodeRuntime `json:"nodes"`
}

type GatewayRoutingNodeRuntime struct {
	ID                  string     `json:"id"`
	Origin              string     `json:"origin"`
	TargetWeight        int        `json:"target_weight"`
	EffectiveWeight     int        `json:"effective_weight"`
	MaxConcurrency      int        `json:"max_concurrency"`
	CurrentConcurrency  *int       `json:"current_concurrency"`
	OverflowFallback    bool       `json:"overflow_fallback"`
	AutoDisabled        bool       `json:"auto_disabled"`
	AutoDisabledReason  string     `json:"auto_disabled_reason,omitempty"`
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

type cachedGatewayRoutingAdmissionSettings struct {
	settings  *GatewayRoutingSettings
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

type gatewayRoutingRecordResult struct {
	index  int
	node   gatewayRoutingMonitorNode
	record gatewayRoutingMonitorRecord
	err    error
}

func DefaultGatewayRoutingSettings() *GatewayRoutingSettings {
	return &GatewayRoutingSettings{
		MonitorURL:               defaultGatewayRoutingMonitorURL,
		TrafficProtectionEnabled: true,
		HealthProtectionEnabled:  true,
		TrafficThresholdPercent:  defaultGatewayRoutingThresholdPercent,
		Nodes: []GatewayRoutingNodeSettings{
			{ID: "bwg-us-01", Origin: "https://control-origin.xiaohondou.com", TargetWeight: 25},
			{ID: "vmiss-us-01", Origin: "https://gateway-origin.xiaohondou.com", TargetWeight: 20},
			{ID: "yt-us-01", Origin: "https://gateway154-origin.xiaohondou.com", TargetWeight: 40},
			{ID: "vmiss-us-02", Origin: "https://gateway2-origin.xiaohondou.com", TargetWeight: 10},
			{ID: "dmit-us-01", Origin: "https://gateway3-origin.xiaohondou.com", TargetWeight: 5},
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
	normalizeStoredGatewayRoutingWeights(settings)
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
	s.gatewayRoutingAdmissionSF.Forget("gateway_routing_admission")
	s.gatewayRoutingAdmissionCache.Store(&cachedGatewayRoutingAdmissionSettings{expiresAt: 0})
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
	settings.OverflowNodeID = strings.ToLower(strings.TrimSpace(settings.OverflowNodeID))
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
		if node.MaxConcurrency < 0 || node.MaxConcurrency > maxGatewayRoutingConcurrency {
			return fmt.Errorf("nodes[%d].max_concurrency must be between 0 and %d", i, maxGatewayRoutingConcurrency)
		}
		totalWeight += node.TargetWeight
	}
	if settings.OverflowNodeID != "" {
		if _, exists := ids[settings.OverflowNodeID]; !exists {
			return errors.New("overflow_node_id must reference a configured node")
		}
	}
	if totalWeight == 0 {
		return errors.New("at least one node target_weight must be greater than zero")
	}
	if totalWeight != 100 {
		return fmt.Errorf("target weights must total 100%% (got %d%%)", totalWeight)
	}
	return nil
}

func cloneGatewayRoutingSettings(source *GatewayRoutingSettings) *GatewayRoutingSettings {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Nodes = append([]GatewayRoutingNodeSettings(nil), source.Nodes...)
	return &cloned
}

// GetGatewayRoutingAdmissionSettings returns a short-lived settings snapshot
// for the request admission hot path without hitting PostgreSQL per request.
func (s *SettingService) GetGatewayRoutingAdmissionSettings(ctx context.Context) (*GatewayRoutingSettings, error) {
	now := time.Now().UTC()
	if cached, ok := s.gatewayRoutingAdmissionCache.Load().(*cachedGatewayRoutingAdmissionSettings); ok && cached != nil && cached.settings != nil && now.UnixNano() < cached.expiresAt {
		return cloneGatewayRoutingSettings(cached.settings), nil
	}
	value, err, _ := s.gatewayRoutingAdmissionSF.Do("gateway_routing_admission", func() (any, error) {
		if cached, ok := s.gatewayRoutingAdmissionCache.Load().(*cachedGatewayRoutingAdmissionSettings); ok && cached != nil && cached.settings != nil && time.Now().UTC().UnixNano() < cached.expiresAt {
			return cloneGatewayRoutingSettings(cached.settings), nil
		}
		settings, loadErr := s.GetGatewayRoutingSettings(ctx)
		if loadErr != nil {
			return nil, loadErr
		}
		s.gatewayRoutingAdmissionCache.Store(&cachedGatewayRoutingAdmissionSettings{
			settings:  cloneGatewayRoutingSettings(settings),
			expiresAt: time.Now().Add(gatewayRoutingAdmissionCacheTTL).UnixNano(),
		})
		return settings, nil
	})
	if err != nil {
		return nil, err
	}
	settings, ok := value.(*GatewayRoutingSettings)
	if !ok || settings == nil {
		return nil, errors.New("gateway routing admission settings are unavailable")
	}
	return cloneGatewayRoutingSettings(settings), nil
}

func (s *SettingService) SetGatewayRoutingCapacityStore(store *GatewayNodeCapacityStore) {
	if s != nil {
		s.gatewayRoutingCapacityStore = store
	}
}

func (s *SettingService) GatewayRoutingNodeMaxConcurrency(ctx context.Context, nodeID string) (int, error) {
	settings, err := s.GetGatewayRoutingAdmissionSettings(ctx)
	if err != nil {
		return 0, err
	}
	for _, node := range settings.Nodes {
		if node.ID == strings.ToLower(strings.TrimSpace(nodeID)) {
			return node.MaxConcurrency, nil
		}
	}
	return 0, nil
}

func (s *SettingService) AcquireGatewayRoutingNodeCapacity(ctx context.Context, nodeID string) (*GatewayNodeCapacityLease, int, int, error) {
	limit, err := s.GatewayRoutingNodeMaxConcurrency(ctx, nodeID)
	if err != nil || limit <= 0 {
		return nil, 0, limit, err
	}
	if s == nil || s.gatewayRoutingCapacityStore == nil {
		return nil, 0, limit, errors.New("gateway node capacity store is unavailable")
	}
	lease, current, err := s.gatewayRoutingCapacityStore.Acquire(ctx, nodeID, limit)
	return lease, current, limit, err
}

// normalizeStoredGatewayRoutingWeights migrates the pre-percentage ratio format
// on read. New writes are rejected unless they already total exactly 100.
func normalizeStoredGatewayRoutingWeights(settings *GatewayRoutingSettings) {
	if settings == nil || len(settings.Nodes) == 0 {
		return
	}

	total := 0
	for _, node := range settings.Nodes {
		if node.TargetWeight < 0 || node.TargetWeight > maxGatewayRoutingWeight {
			return
		}
		total += node.TargetWeight
	}
	if total <= 0 || total == 100 {
		return
	}

	type remainder struct {
		index int
		value int
	}
	remainders := make([]remainder, 0, len(settings.Nodes))
	floorTotal := 0
	for i := range settings.Nodes {
		scaled := settings.Nodes[i].TargetWeight * 100
		settings.Nodes[i].TargetWeight = scaled / total
		floorTotal += settings.Nodes[i].TargetWeight
		remainders = append(remainders, remainder{index: i, value: scaled % total})
	}

	sort.SliceStable(remainders, func(i, j int) bool {
		return remainders[i].value > remainders[j].value
	})
	for i := 0; i < 100-floorTotal && i < len(remainders); i++ {
		settings.Nodes[remainders[i].index].TargetWeight++
	}
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
			runtime := cloneGatewayRoutingRuntime(cached.runtime)
			s.populateGatewayRoutingCapacity(ctx, runtime)
			return runtime, nil
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
	cloned := cloneGatewayRoutingRuntime(runtime)
	s.populateGatewayRoutingCapacity(ctx, cloned)
	return cloned, nil
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
		OverflowNodeID: settings.OverflowNodeID,
		Nodes:          make([]GatewayRoutingNodeRuntime, len(settings.Nodes)),
	}
	type matchedMonitorNode struct {
		index int
		node  gatewayRoutingMonitorNode
	}
	matched := make([]matchedMonitorNode, 0, len(settings.Nodes))
	missing := make([]int, 0)
	for i, configured := range settings.Nodes {
		status := "active"
		if configured.TargetWeight == 0 {
			status = "manual_disabled"
		}
		runtime.Nodes[i] = GatewayRoutingNodeRuntime{
			ID:               configured.ID,
			Origin:           configured.Origin,
			TargetWeight:     configured.TargetWeight,
			EffectiveWeight:  configured.TargetWeight,
			MaxConcurrency:   configured.MaxConcurrency,
			OverflowFallback: configured.ID == settings.OverflowNodeID,
			Status:           status,
		}
		monitorNode, ok := byName[configured.ID]
		if !ok {
			missing = append(missing, i)
			continue
		}
		runtime.Nodes[i].TrafficLimitBytes = monitorNode.TrafficLimit
		runtime.Nodes[i].TrafficLimitType = normalizeGatewayRoutingTrafficLimitType(monitorNode.TrafficLimitType)
		runtime.Nodes[i].Unlimited = monitorNode.TrafficLimit <= 0
		matched = append(matched, matchedMonitorNode{index: i, node: monitorNode})
	}

	group, groupCtx := errgroup.WithContext(ctx)
	results := make([]gatewayRoutingRecordResult, len(matched))
	for resultIndex, item := range matched {
		resultIndex := resultIndex
		item := item
		group.Go(func() error {
			record, fetchErr := fetchGatewayRoutingLatestRecord(groupCtx, client, settings.MonitorURL, item.node.UUID)
			results[resultIndex] = gatewayRoutingRecordResult{
				index:  item.index,
				node:   item.node,
				record: record,
				err:    fetchErr,
			}
			return nil
		})
	}
	_ = group.Wait()

	hasFreshRoutingNode := false
	for _, result := range results {
		if result.err != nil {
			continue
		}
		configured := settings.Nodes[result.index]
		if gatewayRoutingNodeCanReceiveRequests(settings, configured) && gatewayRoutingRecordFreshForHealth(result.record, now) {
			hasFreshRoutingNode = true
			break
		}
	}

	for _, index := range missing {
		configured := settings.Nodes[index]
		applyStaleGatewayRoutingNode(&runtime.Nodes[index], configured, previousGatewayRoutingNode(previous, configured))
		if settings.HealthProtectionEnabled && gatewayRoutingNodeCanReceiveRequests(settings, configured) && hasFreshRoutingNode {
			applyAutoDisabledGatewayRoutingNode(&runtime.Nodes[index], "monitor_missing")
		}
	}

	for _, result := range results {
		configured := settings.Nodes[result.index]
		if result.err != nil {
			applyStaleGatewayRoutingNode(&runtime.Nodes[result.index], configured, previousGatewayRoutingNode(previous, configured))
			if settings.HealthProtectionEnabled && gatewayRoutingNodeCanReceiveRequests(settings, configured) && hasFreshRoutingNode {
				applyAutoDisabledGatewayRoutingNode(&runtime.Nodes[result.index], "monitor_record_unavailable")
			}
			continue
		}
		runtime.Nodes[result.index].MonitorSampleAt = &result.record.Time
		if !gatewayRoutingRecordFreshForHealth(result.record, now) {
			applyStaleGatewayRoutingNode(&runtime.Nodes[result.index], configured, previousGatewayRoutingNode(previous, configured))
			if settings.HealthProtectionEnabled && gatewayRoutingNodeCanReceiveRequests(settings, configured) && hasFreshRoutingNode {
				applyAutoDisabledGatewayRoutingNode(&runtime.Nodes[result.index], "monitor_stale")
			}
			continue
		}
		if !gatewayRoutingRecordFreshForTraffic(result.record, now) {
			applyStaleGatewayRoutingNode(&runtime.Nodes[result.index], configured, previousGatewayRoutingNode(previous, configured))
			continue
		}
		used := gatewayRoutingTrafficUsed(runtime.Nodes[result.index].TrafficLimitType, result.record.NetTotalUp, result.record.NetTotalDown)
		runtime.Nodes[result.index].TrafficUsedBytes = used
		if runtime.Nodes[result.index].Unlimited {
			if configured.TargetWeight == 0 {
				continue
			}
			runtime.Nodes[result.index].Status = "unlimited"
			continue
		}
		percentage := float64(used) / float64(runtime.Nodes[result.index].TrafficLimitBytes) * 100
		runtime.Nodes[result.index].TrafficUsagePercent = &percentage
		if gatewayRoutingNodeCanReceiveRequests(settings, configured) && settings.TrafficProtectionEnabled && percentage >= settings.TrafficThresholdPercent {
			applyAutoDisabledGatewayRoutingNode(&runtime.Nodes[result.index], "traffic_threshold")
		}
	}
	for _, node := range runtime.Nodes {
		if node.MonitorStale {
			runtime.MonitorStale = true
			break
		}
	}
	return runtime, nil
}

func gatewayRoutingNodeCanReceiveRequests(settings *GatewayRoutingSettings, node GatewayRoutingNodeSettings) bool {
	return node.TargetWeight > 0 || (settings != nil && node.ID == settings.OverflowNodeID)
}

func (s *SettingService) populateGatewayRoutingCapacity(ctx context.Context, runtime *GatewayRoutingRuntime) {
	if s == nil || s.gatewayRoutingCapacityStore == nil || runtime == nil {
		return
	}
	for i := range runtime.Nodes {
		count, err := s.gatewayRoutingCapacityStore.Current(ctx, runtime.Nodes[i].ID)
		if err != nil {
			runtime.CapacityError = err.Error()
			return
		}
		runtime.Nodes[i].CurrentConcurrency = &count
	}
}

func gatewayRoutingRecordFreshForHealth(record gatewayRoutingMonitorRecord, now time.Time) bool {
	return gatewayRoutingRecordFresh(record, now, gatewayRoutingHealthStaleAfter)
}

func gatewayRoutingRecordFreshForTraffic(record gatewayRoutingMonitorRecord, now time.Time) bool {
	return gatewayRoutingRecordFresh(record, now, gatewayRoutingMonitorStaleAfter)
}

func gatewayRoutingRecordFresh(record gatewayRoutingMonitorRecord, now time.Time, staleAfter time.Duration) bool {
	return !record.Time.IsZero() && !record.Time.After(now.Add(5*time.Minute)) && now.Sub(record.Time) <= staleAfter
}

func staleGatewayRoutingRuntime(settings *GatewayRoutingSettings, previous *GatewayRoutingRuntime, now time.Time, message string) *GatewayRoutingRuntime {
	runtime := &GatewayRoutingRuntime{
		GeneratedAt:    now,
		MonitorChecked: now,
		MonitorStale:   true,
		MonitorError:   message,
		OverflowNodeID: settings.OverflowNodeID,
		Nodes:          make([]GatewayRoutingNodeRuntime, len(settings.Nodes)),
	}
	for i, node := range settings.Nodes {
		runtime.Nodes[i] = GatewayRoutingNodeRuntime{
			ID:               node.ID,
			Origin:           node.Origin,
			TargetWeight:     node.TargetWeight,
			EffectiveWeight:  node.TargetWeight,
			MaxConcurrency:   node.MaxConcurrency,
			OverflowFallback: node.ID == settings.OverflowNodeID,
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
		runtime.AutoDisabledReason = previous.AutoDisabledReason
		if previous.AutoDisabled {
			runtime.EffectiveWeight = 0
			runtime.AutoDisabled = true
			runtime.Status = "auto_disabled"
		}
	}
	if configured.TargetWeight == 0 {
		runtime.EffectiveWeight = 0
		runtime.AutoDisabled = false
		runtime.AutoDisabledReason = ""
		runtime.Status = "manual_disabled"
	}
}

func applyAutoDisabledGatewayRoutingNode(runtime *GatewayRoutingNodeRuntime, reason string) {
	if runtime == nil {
		return
	}
	runtime.EffectiveWeight = 0
	runtime.AutoDisabled = true
	runtime.AutoDisabledReason = reason
	if reason == "" || reason == "traffic_threshold" {
		runtime.Status = "auto_disabled"
		return
	}
	runtime.Status = "auto_disabled_" + reason
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
	defer func() { _ = resp.Body.Close() }()
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
