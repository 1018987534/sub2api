package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetTotalDurationCircuitBreakerSettings(c *gin.Context) {
	settings, err := h.settingService.GetTotalDurationCircuitBreakerSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateTotalDurationCircuitBreakerSettings(c *gin.Context) {
	var req struct {
		MaxSlowRequests  *int `json:"max_slow_requests" binding:"required,gte=0,lte=9999"`
		ThresholdSeconds *int `json:"threshold_seconds" binding:"required,gte=1,lte=86400"`
		WindowSeconds    *int `json:"window_seconds" binding:"required,gte=1,lte=86400"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "max_slow_requests must be 0-9999, threshold_seconds must be 1-86400, and window_seconds must be 1-86400")
		return
	}
	settings, err := h.settingService.UpdateTotalDurationCircuitBreakerSettings(c.Request.Context(), *req.MaxSlowRequests, *req.ThresholdSeconds, *req.WindowSeconds)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
