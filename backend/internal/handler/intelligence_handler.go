package handler

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type IntelligenceHandler struct {
	store         intelligence.Store
	runner        *intelligence.Runner
	recovery      *intelligence.RecoveryRunner
	recoveryAdmin intelligence.RecoveryAdminStore
	keys          service.APIKeyRepository
	groups        service.GroupRepository
	access        channelMonitorV2GroupAuthorizer
	monitor       *service.ChannelMonitorV2Service
	settings      *service.SettingService
}

func NewIntelligenceHandler(db *sql.DB, cfg *config.Config, keys service.APIKeyRepository, groups service.GroupRepository, access *service.APIKeyService, settings *service.SettingService, monitor *service.ChannelMonitorV2Service, gateway *service.OpenAIGatewayService) *IntelligenceHandler {
	store := &intelligence.SQLStore{DB: db, PauseThreshold: settings.GetIntelligencePauseThreshold}
	h := &IntelligenceHandler{store: store, keys: keys, groups: groups, access: access, monitor: monitor, settings: settings}
	recoveryStore := &intelligence.SQLRecoveryStore{DB: db}
	h.recoveryAdmin = recoveryStore
	if gateway != nil && groups != nil {
		store.ProtectionEligible = func(ctx context.Context, c intelligence.Config) (bool, error) {
			group, err := groups.GetByID(ctx, c.GroupID)
			return service.IntelligenceProtectionGroupEligible(group), err
		}
		store.Recover = gateway.RecoverIntelligenceAccount
		store.RecoveryCutoff = gateway.IntelligenceRecoveryCutoff
		store.Protect = func(ctx context.Context, c intelligence.Config, accountID int64) (bool, error) {
			group, err := groups.GetByID(ctx, c.GroupID)
			if err != nil {
				return false, err
			}
			return gateway.ProtectDegradedIntelligenceAccount(ctx, group, accountID, c.Model, c.UpdatedBy)
		}
	}
	enabled := func(ctx context.Context) bool {
		if !cfg.IsControl() || settings == nil || !settings.GetChannelMonitorRuntime(ctx).PassiveAggregationAllowed() {
			return false
		}
		c, err := monitor.GetConfig(ctx)
		return err == nil && c.Enabled
	}
	var routing intelligenceRoutingProvider
	if settings != nil {
		routing = settings
	}
	probe := &intelligence.HTTPProbe{ResolveEndpoint: intelligenceProbeEndpoint(cfg, routing), Resolve: h.credential}
	h.runner = intelligence.NewRunner(h.store, probe, enabled)
	if gateway != nil && db != nil {
		recoveryStore.Recover = gateway.RecoverIntelligenceAccount
		h.recovery = intelligence.NewRecoveryRunner(recoveryStore, gateway, func(ctx context.Context) (intelligence.Config, error) {
			// Recovery is independent of the public monitor switch. Reuse one
			// enabled OpenAI group's saved test, without routing through its pool.
			configs, err := h.store.Configs(ctx)
			if err != nil {
				return intelligence.Config{}, err
			}
			for _, c := range configs {
				if c.ProbeID != 0 || !c.Enabled || c.Validate() != nil {
					continue
				}
				group, err := groups.GetByID(ctx, c.GroupID)
				if err == nil && group != nil && group.Platform == service.PlatformOpenAI && group.Status == service.StatusActive {
					return c, nil
				}
			}
			return intelligence.Config{}, intelligence.ErrInvalid
		})
	}
	if cfg.IsControl() && os.Getenv("INTELLIGENCE_CHECKS_DISABLE_RUNNER") != "1" {
		h.runner.Start()
		if h.recovery != nil {
			h.recovery.Start()
		}
	}
	return h
}
func (h *IntelligenceHandler) Stop() {
	if h != nil && h.runner != nil {
		h.runner.Stop()
	}
	if h != nil && h.recovery != nil {
		h.recovery.Stop()
	}
}
func (h *IntelligenceHandler) credential(ctx context.Context, c intelligence.Config) (string, error) {
	key, err := h.keys.GetByID(ctx, c.APIKeyID)
	if err != nil {
		return "", err
	}
	if key == nil || key.Key == "" || key.GroupID == nil || *key.GroupID != c.GroupID || key.UserID != c.UpdatedBy || !key.IsActive() || key.IsExpired() || key.IsQuotaExhausted() {
		return "", intelligence.ErrInvalid
	}
	group, err := h.groups.GetByID(ctx, c.GroupID)
	if err != nil {
		return "", err
	}
	if group == nil || !intelligence.GroupEligible(group.Name, group.Status) {
		return "", intelligence.ErrInvalid
	}
	return key.Key, nil
}
func (h *IntelligenceHandler) Configs(c *gin.Context) {
	items, err := h.store.Configs(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取降智检测配置")
		return
	}
	threshold, err := h.settings.GetIntelligencePauseThreshold(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取降智暂停门槛")
		return
	}
	response.Success(c, gin.H{
		"items": items, "defaults": intelligence.DefaultConfig(0), "max_models_per_group": intelligence.MaxGroupModels,
		"pause_threshold": threshold, "max_pause_threshold": intelligence.MaxPauseThreshold,
	})
}

func (h *IntelligenceHandler) UpdatePauseThreshold(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		PauseThreshold int `json:"pause_threshold"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, intelligence.ErrInvalidPauseThreshold.Error())
		return
	}
	err := h.settings.SetIntelligencePauseThreshold(c.Request.Context(), input.PauseThreshold)
	if errors.Is(err, intelligence.ErrInvalidPauseThreshold) {
		response.BadRequest(c, err.Error())
		return
	}
	if err != nil {
		response.InternalError(c, "无法保存降智暂停门槛")
		return
	}
	response.Success(c, gin.H{"pause_threshold": input.PauseThreshold})
}
func intelligenceGroupID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("groupID"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid group id")
		return 0, false
	}
	return id, true
}
func (h *IntelligenceHandler) Save(c *gin.Context) {
	h.saveConfig(c, 0, false)
}

func (h *IntelligenceHandler) AddModel(c *gin.Context) {
	h.saveConfig(c, 0, true)
}

func (h *IntelligenceHandler) SaveModel(c *gin.Context) {
	if probeID, ok := intelligenceModelID(c); ok {
		h.saveConfig(c, probeID, false)
	}
}

func intelligenceModelID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("probeID"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid probe id")
		return 0, false
	}
	return id, true
}

func intelligenceProbeKey(c *gin.Context) (intelligence.ProbeKey, bool) {
	groupID, ok := intelligenceGroupID(c)
	if !ok {
		return intelligence.ProbeKey{}, false
	}
	key := intelligence.ProbeKey{GroupID: groupID}
	if raw := c.Query("probe_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 0 {
			response.BadRequest(c, "invalid probe id")
			return intelligence.ProbeKey{}, false
		}
		key.ProbeID = id
	}
	return key, true
}

func (h *IntelligenceHandler) saveConfig(c *gin.Context, probeID int64, addModel bool) {
	id, ok := intelligenceGroupID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var input intelligence.Config
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid config")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	input.GroupID = id
	input.ProbeID = probeID
	input.UpdatedBy = subject.UserID
	if err := input.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	group, err := h.groups.GetByID(c.Request.Context(), id)
	if err != nil || group == nil {
		response.NotFound(c, "group not found")
		return
	}
	if !intelligence.GroupEligible(group.Name, group.Status) {
		response.BadRequest(c, "仅支持名称包含不降智且状态为启用的分组")
		return
	}
	if input.APIKeyID > 0 {
		if _, err := h.credential(c.Request.Context(), input); err != nil {
			response.BadRequest(c, "请选择当前管理员拥有、有效且绑定本分组的专用 API Key")
			return
		}
	}
	var saved intelligence.Config
	if addModel {
		saved, err = h.store.AddModel(c.Request.Context(), input)
	} else {
		saved, err = h.store.Save(c.Request.Context(), input)
	}
	if errors.Is(err, intelligence.ErrConflict) {
		response.Error(c, 409, "检测配置已变更，或尚未保存主检测，请刷新后重试")
		return
	}
	if errors.Is(err, intelligence.ErrDuplicateModel) || errors.Is(err, intelligence.ErrModelLimit) || errors.Is(err, intelligence.ErrInvalid) {
		response.BadRequest(c, err.Error())
		return
	}
	if err != nil {
		response.InternalError(c, "无法保存降智检测配置")
		return
	}
	response.Success(c, saved)
}

func (h *IntelligenceHandler) DeleteModel(c *gin.Context) {
	id, ok := intelligenceGroupID(c)
	if !ok {
		return
	}
	probeID, ok := intelligenceModelID(c)
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Query("version"))
	if err != nil || version <= 0 {
		response.BadRequest(c, "invalid version")
		return
	}
	err = h.store.DeleteModel(c.Request.Context(), intelligence.ProbeKey{GroupID: id, ProbeID: probeID}, version)
	if errors.Is(err, intelligence.ErrConflict) {
		response.Error(c, 409, "检测正在运行或配置已变更，请稍后刷新重试")
		return
	}
	if err != nil {
		response.InternalError(c, "无法删除降智检测配置")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *IntelligenceHandler) Run(c *gin.Context) {
	key, ok := intelligenceProbeKey(c)
	if !ok {
		return
	}
	group, err := h.groups.GetByID(c.Request.Context(), key.GroupID)
	if err != nil || group == nil {
		response.NotFound(c, "group not found")
		return
	}
	if !intelligence.GroupEligible(group.Name, group.Status) {
		response.BadRequest(c, "仅支持名称包含不降智且状态为启用的分组")
		return
	}
	err = h.runner.RunNow(c.Request.Context(), key)
	if errors.Is(err, intelligence.ErrConflict) {
		response.Error(c, 409, "检测正在运行、队列已满或配置尚未保存")
		return
	}
	if err != nil {
		response.BadRequest(c, "检测未启动，请确认 V2 已启用和配置有效")
		return
	}
	response.Success(c, gin.H{"started": true})
}
func (h *IntelligenceHandler) History(c *gin.Context) {
	key, ok := intelligenceProbeKey(c)
	if !ok {
		return
	}
	before := int64(0)
	if raw := c.Query("before_id"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			response.BadRequest(c, "invalid history cursor")
			return
		}
	}
	rows, err := h.store.History(c.Request.Context(), key, time.Now().Add(-7*24*time.Hour), before, 25)
	if err != nil {
		response.InternalError(c, "无法读取检测记录")
		return
	}
	more := len(rows) > 24
	if more {
		rows = rows[:24]
	}
	response.Success(c, gin.H{"items": rows, "has_more": more})
}
func (h *IntelligenceHandler) statusGroupScope(c *gin.Context, userID int64) (map[int64]bool, bool) {
	groups, err := h.access.GetAvailableGroups(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "无法读取可用分组")
		return nil, false
	}
	cfg, err := h.monitor.GetConfig(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取监控配置")
		return nil, false
	}
	if cfg == nil || !cfg.Enabled {
		response.Forbidden(c, "V2 monitor is disabled")
		return nil, false
	}
	configured := map[int64]bool{}
	for _, id := range cfg.GroupIDs {
		configured[id] = true
	}
	platforms := map[string]bool{}
	for _, p := range cfg.Platforms {
		if p.Enabled {
			platforms[p.Platform] = true
		}
	}
	allowed := map[int64]bool{}
	for _, g := range groups {
		if platforms[g.Platform] && (len(configured) == 0 || configured[g.ID]) {
			allowed[g.ID] = true
		}
	}
	return allowed, true
}

// RecordDetail returns only the requested answer and its saved display metadata.
// Request timing, credentials, account identity and recovery details stay private.
func (h *IntelligenceHandler) RecordDetail(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	groupID, ok := intelligenceGroupID(c)
	if !ok {
		return
	}
	recordID, err := strconv.ParseInt(c.Param("recordID"), 10, 64)
	if err != nil || recordID <= 0 || recordID == math.MaxInt64 {
		response.BadRequest(c, "invalid record id")
		return
	}
	allowed, ok := h.statusGroupScope(c, subject.UserID)
	if !ok {
		return
	}
	if !allowed[groupID] {
		response.NotFound(c, "检测记录不存在或已过期")
		return
	}
	record, err := h.store.Record(c.Request.Context(), groupID, recordID, time.Now().Add(-7*24*time.Hour))
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "检测记录不存在或已过期")
		return
	}
	if err != nil {
		response.InternalError(c, "无法读取检测回答")
		return
	}
	model, effort := "", ""
	if record.Config != nil {
		model, effort = record.Config.Model, record.Config.ReasoningEffort
	}
	response.Success(c, gin.H{
		"id": record.ID, "group_id": record.GroupID, "status": record.Status,
		"answer": record.Answer, "model": model, "reasoning_effort": effort,
	})
}

type intelligenceDisplayMetadata struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	Protocol        string `json:"protocol"`
	IntervalMinutes int    `json:"interval_minutes"`
	Enabled         bool   `json:"enabled"`
}

type intelligenceModelTimeline struct {
	intelligenceDisplayMetadata
	ProbeID int64                         `json:"probe_id"`
	Records []intelligence.TimelineRecord `json:"records"`
}

func (h *IntelligenceHandler) Status(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	requested := c.QueryArray("group_id")
	if len(requested) > 100 {
		response.BadRequest(c, "too many groups")
		return
	}
	ids := map[int64]bool{}
	for _, raw := range requested {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "invalid group id")
			return
		}
		ids[id] = true
	}
	allowed, ok := h.statusGroupScope(c, subject.UserID)
	if !ok {
		return
	}
	items, err := h.store.Configs(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取检测状态")
		return
	}
	var visible []intelligence.Config
	for _, cfg := range items {
		if allowed[cfg.GroupID] && ids[cfg.GroupID] {
			visible = append(visible, cfg)
		}
	}
	const recordLimit = 60
	now := time.Now()
	timelines, err := h.store.Timelines(c.Request.Context(), visible, now.Add(-7*24*time.Hour), recordLimit)
	if err != nil {
		response.InternalError(c, "无法读取检测状态")
		return
	}
	groups := map[int64][]intelligence.TimelineRecord{}
	metadata := map[int64]intelligenceDisplayMetadata{}
	models := map[int64][]intelligenceModelTimeline{}
	for _, cfg := range visible {
		records := timelines[cfg.Key()]
		if records == nil {
			records = []intelligence.TimelineRecord{}
		}
		meta := intelligenceDisplayMetadata{cfg.Model, cfg.ReasoningEffort, cfg.Protocol, cfg.IntervalMinutes, cfg.Enabled}
		if cfg.ProbeID == 0 {
			// Preserve the primary response fields for pages already open during rollout.
			groups[cfg.GroupID], metadata[cfg.GroupID] = records, meta
		} else {
			models[cfg.GroupID] = append(models[cfg.GroupID], intelligenceModelTimeline{meta, cfg.ProbeID, records})
		}
	}
	response.Success(c, gin.H{"groups": groups, "metadata": metadata, "models": models, "window_minutes": 7 * 24 * 60, "record_limit": recordLimit, "server_time": now.UTC()})
}
