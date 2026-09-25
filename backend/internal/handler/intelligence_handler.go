package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	store   intelligence.Store
	runner  *intelligence.Runner
	keys    service.APIKeyRepository
	groups  service.GroupRepository
	access  channelMonitorV2GroupAuthorizer
	monitor *service.ChannelMonitorV2Service
}

func NewIntelligenceHandler(db *sql.DB, cfg *config.Config, keys service.APIKeyRepository, groups service.GroupRepository, access *service.APIKeyService, settings *service.SettingService, monitor *service.ChannelMonitorV2Service) *IntelligenceHandler {
	h := &IntelligenceHandler{store: &intelligence.SQLStore{DB: db}, keys: keys, groups: groups, access: access, monitor: monitor}
	enabled := func(ctx context.Context) bool {
		if !cfg.IsControl() || settings == nil || !settings.GetChannelMonitorRuntime(ctx).PassiveAggregationAllowed() {
			return false
		}
		c, err := monitor.GetConfig(ctx)
		return err == nil && c.Enabled
	}
	probe := &intelligence.HTTPProbe{Endpoint: fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port), Resolve: h.credential}
	h.runner = intelligence.NewRunner(h.store, probe, enabled)
	if cfg.IsControl() && os.Getenv("INTELLIGENCE_CHECKS_DISABLE_RUNNER") != "1" {
		h.runner.Start()
	}
	return h
}
func (h *IntelligenceHandler) Stop() {
	if h != nil && h.runner != nil {
		h.runner.Stop()
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
	if group.Status != service.StatusActive {
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
	response.Success(c, gin.H{"items": items, "defaults": intelligence.DefaultConfig(0)})
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
	input.UpdatedBy = subject.UserID
	if err := input.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if _, err := h.groups.GetByID(c.Request.Context(), id); err != nil {
		response.NotFound(c, "group not found")
		return
	}
	if input.APIKeyID > 0 {
		if _, err := h.credential(c.Request.Context(), input); err != nil {
			response.BadRequest(c, "请选择当前管理员拥有、有效且绑定本分组的专用 API Key")
			return
		}
	}
	saved, err := h.store.Save(c.Request.Context(), input)
	if errors.Is(err, intelligence.ErrConflict) {
		response.Error(c, 409, err.Error())
		return
	}
	if err != nil {
		response.InternalError(c, "无法保存降智检测配置")
		return
	}
	response.Success(c, saved)
}
func (h *IntelligenceHandler) Run(c *gin.Context) {
	id, ok := intelligenceGroupID(c)
	if !ok {
		return
	}
	err := h.runner.RunNow(c.Request.Context(), id)
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
	id, ok := intelligenceGroupID(c)
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
	rows, err := h.store.History(c.Request.Context(), id, time.Now().Add(-7*24*time.Hour), before, 25)
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
	groups, err := h.access.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.InternalError(c, "无法读取可用分组")
		return
	}
	cfg, err := h.monitor.GetConfig(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取监控配置")
		return
	}
	if cfg == nil || !cfg.Enabled {
		response.Forbidden(c, "V2 monitor is disabled")
		return
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
	items, err := h.store.Configs(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取检测状态")
		return
	}
	result := map[int64][]intelligence.Record{}
	metadata := map[int64]gin.H{}
	now := time.Now()
	for _, cfg := range items {
		if !allowed[cfg.GroupID] || !ids[cfg.GroupID] {
			continue
		}
		records, err := h.store.History(c.Request.Context(), cfg.GroupID, now.Add(-7*24*time.Hour), 0, 60)
		if err != nil {
			response.InternalError(c, "无法读取检测状态")
			return
		}
		// Strip prompts, answers, errors, key IDs and operator IDs server-side.
		for i := range records {
			records[i].Answer = ""
			records[i].Error = ""
			records[i].Config = nil
		}
		result[cfg.GroupID] = records
		// Expose only display metadata for requested, authorized groups.
		metadata[cfg.GroupID] = gin.H{"model": cfg.Model, "reasoning_effort": cfg.ReasoningEffort}
	}
	response.Success(c, gin.H{"groups": result, "metadata": metadata, "window_minutes": 7 * 24 * 60, "record_limit": 60, "server_time": now.UTC()})
}
