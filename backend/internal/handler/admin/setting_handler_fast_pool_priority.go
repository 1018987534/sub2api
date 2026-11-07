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
		Mode                      service.FastPoolPriorityMode `json:"mode" binding:"required,oneof=intelligence latency rate"`
		CacheRateThresholdPercent json.RawMessage              `json:"cache_rate_threshold_percent"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "mode must be intelligence, latency, or rate")
		return
	}
	var threshold *float64
	if len(req.CacheRateThresholdPercent) == 0 {
		// Older clients save only the mode; preserve the current optional threshold.
		current, err := h.settingService.GetFastPoolPrioritySettings(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		threshold = current.CacheRateThresholdPercent
	} else if err := json.Unmarshal(req.CacheRateThresholdPercent, &threshold); err != nil {
		response.BadRequest(c, "cache_rate_threshold_percent must be null or a percentage from 0 to 100")
		return
	}
	settings, err := h.settingService.UpdateFastPoolPrioritySettings(c.Request.Context(), req.Mode, threshold)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
