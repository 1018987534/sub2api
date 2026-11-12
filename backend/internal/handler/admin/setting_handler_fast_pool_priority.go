package admin

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetFastPoolPrioritySettings(c *gin.Context) {
	settings, err := h.settingService.GetFastPoolPrioritySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateFastPoolPrioritySettings(c *gin.Context) {
	var req struct {
		Mode                      service.FastPoolPriorityMode  `json:"mode" binding:"omitempty,oneof=intelligence latency rate"`
		BelowThresholdMode        *service.FastPoolPriorityMode `json:"below_threshold_mode" binding:"omitempty,oneof=intelligence latency rate"`
		CacheRateThresholdPercent json.RawMessage               `json:"cache_rate_threshold_percent"`
		GroupID                   *int64                        `json:"group_id"`
		CostEffectivenessEnabled  *bool                         `json:"cost_effectiveness_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "mode and below_threshold_mode must be intelligence, latency, or rate")
		return
	}
	if req.GroupID != nil {
		if req.CostEffectivenessEnabled == nil || req.Mode != "" || req.BelowThresholdMode != nil || len(req.CacheRateThresholdPercent) != 0 {
			response.BadRequest(c, "a group override requires cost_effectiveness_enabled and cannot change global strategies")
			return
		}
		settings, err := h.settingService.UpdateFastPoolCostEffectivenessGroup(c.Request.Context(), *req.GroupID, *req.CostEffectivenessEnabled)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, settings)
		return
	}
	if req.CostEffectivenessEnabled != nil {
		response.BadRequest(c, "group_id is required for a group override")
		return
	}
	var threshold *float64
	belowThresholdMode := service.FastPoolPriorityIntelligence
	if len(req.CacheRateThresholdPercent) == 0 || req.BelowThresholdMode == nil {
		// Older clients omit the new strategy or threshold; preserve saved values.
		current, err := h.settingService.GetFastPoolPrioritySettings(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		threshold = current.CacheRateThresholdPercent
		belowThresholdMode = current.BelowThresholdMode
	}
	if req.BelowThresholdMode != nil {
		belowThresholdMode = *req.BelowThresholdMode
	}
	if len(req.CacheRateThresholdPercent) != 0 {
		if err := json.Unmarshal(req.CacheRateThresholdPercent, &threshold); err != nil {
			response.BadRequest(c, "cache_rate_threshold_percent must be null or a percentage from 0 to 100")
			return
		}
	}
	settings, err := h.settingService.UpdateFastPoolPrioritySettings(c.Request.Context(), req.Mode, threshold, belowThresholdMode)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
