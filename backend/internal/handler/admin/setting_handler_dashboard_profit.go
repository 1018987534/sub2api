package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
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
	var req struct {
		MonthlyServerCostUSD *float64 `json:"monthly_server_cost_usd" binding:"required,gte=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "monthly_server_cost_usd must be a non-negative amount")
		return
	}
	settings, err := h.settingService.UpdateDashboardProfitSettings(c.Request.Context(), *req.MonthlyServerCostUSD)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
