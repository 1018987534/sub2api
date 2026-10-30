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
		MaxSlowRequests *int `json:"max_slow_requests" binding:"required,gte=0,lte=9999"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "max_slow_requests must be an integer between 0 and 9999")
		return
	}
	settings, err := h.settingService.UpdateTotalDurationCircuitBreakerSettings(c.Request.Context(), *req.MaxSlowRequests)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
