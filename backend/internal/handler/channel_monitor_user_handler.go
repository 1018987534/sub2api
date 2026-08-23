package handler

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ChannelMonitorUserHandler 渠道监控用户只读 handler。
type ChannelMonitorUserHandler struct {
	monitorService *service.ChannelMonitorService
	channelService *service.ChannelService
	settingService *service.SettingService
}

// NewChannelMonitorUserHandler 创建 handler。
// settingService 用于每次请求前读取功能开关；关闭时 List/GetStatus 直接返回空/404。
func NewChannelMonitorUserHandler(
	monitorService *service.ChannelMonitorService,
	channelService *service.ChannelService,
	settingService *service.SettingService,
) *ChannelMonitorUserHandler {
	return &ChannelMonitorUserHandler{
		monitorService: monitorService,
		channelService: channelService,
		settingService: settingService,
	}
}

// featureEnabled 返回当前渠道监控功能是否开启。
// settingService 为 nil（测试场景）视为启用。
func (h *ChannelMonitorUserHandler) featureEnabled(c *gin.Context) bool {
	if h.settingService == nil {
		return true
	}
	runtime := h.settingService.GetChannelMonitorRuntime(c.Request.Context())
	return runtime.Enabled && runtime.Mode == service.ChannelMonitorModeV1
}

// quotaVisible 返回用户端是否展示配额/余额快照（channel_monitor_show_quota，
// fail-closed：未配置/非 "true" 一律视为关闭）。settingService 为 nil 时 fail-closed。
func (h *ChannelMonitorUserHandler) quotaVisible(c *gin.Context) bool {
	if h.settingService == nil {
		return false
	}
	return h.settingService.GetChannelMonitorRuntime(c.Request.Context()).ShowQuota
}

// --- Response ---

type channelMonitorUserListItem struct {
	ID                         int64                                `json:"id"`
	Name                       string                               `json:"name"`
	Provider                   string                               `json:"provider"`
	GroupName                  string                               `json:"group_name"`
	PrimaryModel               string                               `json:"primary_model"`
	PrimaryStatus              string                               `json:"primary_status"`
	PrimaryLatencyMs           *int                                 `json:"primary_latency_ms"`
	PrimaryPingLatencyMs       *int                                 `json:"primary_ping_latency_ms"`
	Availability7d             float64                              `json:"availability_7d"`
	ExtraModels                []dto.ChannelMonitorExtraModelStatus `json:"extra_models"`
	Timeline                   []channelMonitorUserTimelinePoint    `json:"timeline"`
	GroupFirstTokenP50Ms       *int64                               `json:"group_first_token_p50_ms,omitempty"`
	GroupFirstTokenSampleCount int64                                `json:"group_first_token_sample_count"`
	GroupCacheRate             *float64                             `json:"group_cache_rate,omitempty"`
	ModelPreview               []channelMonitorUserModelPricing     `json:"model_preview,omitempty"`
	ModelCount                 int                                  `json:"model_count"`
	// LatestQuota 主模型最近配额快照；channel_monitor_show_quota=false 时
	// 由 userMonitorViewToItem 的调用方传入 false 剥离（服务端脱敏，非仅前端隐藏）。
	LatestQuota *domain.MonitorQuotaSnapshot `json:"latest_quota,omitempty"`
}

// channelMonitorUserModelPricing contains only the public model identity and
// the official reference price. Customer-specific channel/order pricing is
// intentionally omitted from the monitor surface.
type channelMonitorUserModelPricing struct {
	Name            string                     `json:"name"`
	Platform        string                     `json:"platform"`
	OfficialPricing *modelPlazaOfficialPricing `json:"official_pricing"`
}

// channelMonitorUserTimelinePoint 主模型最近一次检测的 timeline 点。
// 仅用于用户视图 list 响应，admin 视图不使用。
type channelMonitorUserTimelinePoint struct {
	Status        string `json:"status"`
	LatencyMs     *int   `json:"latency_ms"`
	PingLatencyMs *int   `json:"ping_latency_ms"`
	CheckedAt     string `json:"checked_at"`
}

type channelMonitorUserDetailResponse struct {
	ID            int64                            `json:"id"`
	Name          string                           `json:"name"`
	Provider      string                           `json:"provider"`
	GroupName     string                           `json:"group_name"`
	Models        []channelMonitorUserModelStat    `json:"models"`
	PricingModels []channelMonitorUserModelPricing `json:"pricing_models"`
}

type channelMonitorUserModelStat struct {
	Model           string  `json:"model"`
	LatestStatus    string  `json:"latest_status"`
	LatestLatencyMs *int    `json:"latest_latency_ms"`
	Availability7d  float64 `json:"availability_7d"`
	Availability15d float64 `json:"availability_15d"`
	Availability30d float64 `json:"availability_30d"`
	AvgLatency7dMs  *int    `json:"avg_latency_7d_ms"`
}

func userMonitorViewToItem(v *service.UserMonitorView, includeQuota bool) channelMonitorUserListItem {
	extras := make([]dto.ChannelMonitorExtraModelStatus, 0, len(v.ExtraModels))
	for _, e := range v.ExtraModels {
		extras = append(extras, dto.ChannelMonitorExtraModelStatus{
			Model:     e.Model,
			Status:    e.Status,
			LatencyMs: e.LatencyMs,
		})
	}
	timeline := make([]channelMonitorUserTimelinePoint, 0, len(v.Timeline))
	for _, p := range v.Timeline {
		timeline = append(timeline, channelMonitorUserTimelinePoint{
			Status:        p.Status,
			LatencyMs:     p.LatencyMs,
			PingLatencyMs: p.PingLatencyMs,
			CheckedAt:     p.CheckedAt.UTC().Format(time.RFC3339),
		})
	}
	item := channelMonitorUserListItem{
		ID:                         v.ID,
		Name:                       v.Name,
		Provider:                   v.Provider,
		GroupName:                  v.GroupName,
		PrimaryModel:               v.PrimaryModel,
		PrimaryStatus:              v.PrimaryStatus,
		PrimaryLatencyMs:           v.PrimaryLatencyMs,
		PrimaryPingLatencyMs:       v.PrimaryPingLatencyMs,
		Availability7d:             v.Availability7d,
		ExtraModels:                extras,
		Timeline:                   timeline,
		GroupFirstTokenP50Ms:       v.GroupFirstTokenP50Ms,
		GroupFirstTokenSampleCount: v.GroupFirstTokenSampleCount,
		GroupCacheRate:             v.GroupCacheRate,
	}
	if includeQuota {
		item.LatestQuota = v.LatestQuota
	}
	return item
}

func toMonitorModelPricing(models []service.PlazaModel) []channelMonitorUserModelPricing {
	out := make([]channelMonitorUserModelPricing, 0, len(models))
	for _, model := range models {
		out = append(out, channelMonitorUserModelPricing{
			Name:            model.Name,
			Platform:        model.Platform,
			OfficialPricing: toModelPlazaOfficialPricing(model.OfficialPricing),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func monitorGroupKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "monitor")
	value = strings.TrimSuffix(value, "监控")
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}

func monitorProviderMatches(provider, groupPlatform string) bool {
	return groupPlatform == "composite" || strings.EqualFold(provider, groupPlatform)
}

func findMonitorPlazaGroup(view *service.UserMonitorView, groups []service.PlazaGroup) *service.PlazaGroup {
	if view == nil {
		return nil
	}
	if explicit := strings.TrimSpace(view.GroupName); explicit != "" {
		for i := range groups {
			if monitorProviderMatches(view.Provider, groups[i].Platform) && groups[i].Name == explicit {
				return &groups[i]
			}
		}
	}
	key := monitorGroupKey(view.Name)
	if key == "" {
		return nil
	}
	for i := range groups {
		if !monitorProviderMatches(view.Provider, groups[i].Platform) {
			continue
		}
		groupKey := monitorGroupKey(groups[i].Name)
		if groupKey == key || strings.HasPrefix(groupKey, key) {
			return &groups[i]
		}
	}
	return nil
}

func monitorPricingModels(view *service.UserMonitorView, groups []service.PlazaGroup) []channelMonitorUserModelPricing {
	if view == nil {
		return nil
	}
	if group := findMonitorPlazaGroup(view, groups); group != nil && len(group.Models) > 0 {
		return toMonitorModelPricing(group.Models)
	}
	return nil
}

func (h *ChannelMonitorUserHandler) loadMonitorPricingCatalog(ctx context.Context) []service.PlazaGroup {
	if h == nil || h.channelService == nil {
		return nil
	}
	groups, err := h.channelService.ListPlazaGroups(ctx)
	if err != nil {
		return nil
	}
	return groups
}

func userMonitorDetailToResponse(d *service.UserMonitorDetail) *channelMonitorUserDetailResponse {
	models := make([]channelMonitorUserModelStat, 0, len(d.Models))
	for _, m := range d.Models {
		models = append(models, channelMonitorUserModelStat{
			Model:           m.Model,
			LatestStatus:    m.LatestStatus,
			LatestLatencyMs: m.LatestLatencyMs,
			Availability7d:  m.Availability7d,
			Availability15d: m.Availability15d,
			Availability30d: m.Availability30d,
			AvgLatency7dMs:  m.AvgLatency7dMs,
		})
	}
	return &channelMonitorUserDetailResponse{
		ID:        d.ID,
		Name:      d.Name,
		Provider:  d.Provider,
		GroupName: d.GroupName,
		Models:    models,
	}
}

// --- Handlers ---

// List GET /api/v1/channel-monitors
func (h *ChannelMonitorUserHandler) List(c *gin.Context) {
	if !h.featureEnabled(c) {
		response.Success(c, gin.H{"items": []channelMonitorUserListItem{}})
		return
	}
	views, err := h.monitorService.ListUserView(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	includeQuota := h.quotaVisible(c)
	catalog := h.loadMonitorPricingCatalog(c.Request.Context())
	items := make([]channelMonitorUserListItem, 0, len(views))
	for _, v := range views {
		item := userMonitorViewToItem(v, includeQuota)
		models := monitorPricingModels(v, catalog)
		item.ModelCount = len(models)
		item.ModelPreview = models
		items = append(items, item)
	}
	response.Success(c, gin.H{"items": items})
}

// GetStatus GET /api/v1/channel-monitors/:id/status
func (h *ChannelMonitorUserHandler) GetStatus(c *gin.Context) {
	if !h.featureEnabled(c) {
		response.ErrorFrom(c, service.ErrChannelMonitorNotFound)
		return
	}
	// 复用 admin.ParseChannelMonitorID 保持错误码与日志一致。
	id, ok := admin.ParseChannelMonitorID(c)
	if !ok {
		return
	}
	detail, err := h.monitorService.GetUserDetail(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := userMonitorDetailToResponse(detail)
	view := &service.UserMonitorView{
		ID:        detail.ID,
		Name:      detail.Name,
		Provider:  detail.Provider,
		GroupName: detail.GroupName,
	}
	if len(detail.Models) > 0 {
		view.PrimaryModel = detail.Models[0].Model
		for _, model := range detail.Models[1:] {
			view.ExtraModels = append(view.ExtraModels, service.ExtraModelStatus{Model: model.Model})
		}
	}
	out.PricingModels = monitorPricingModels(view, h.loadMonitorPricingCatalog(c.Request.Context()))
	response.Success(c, out)
}
