package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetDashboardProfitSettings(c *gin.Context) {
	settings, err := h.settingService.GetDashboardProfitSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateDashboardProfitSettings(c *gin.Context) {
	var req service.DashboardProfitSettingsUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "provide a non-negative monthly_server_cost_usd or withdrawal_fee_rate_percent between 0 and 100")
		return
	}
	settings, err := h.settingService.UpdateDashboardProfitSettings(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
