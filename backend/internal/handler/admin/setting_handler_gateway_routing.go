package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type GatewayRoutingAdminResponse struct {
	Settings *service.GatewayRoutingSettings `json:"settings"`
	Runtime  *service.GatewayRoutingRuntime  `json:"runtime"`
}

// GetGatewayRoutingSettings returns administrator targets and current effective weights.
func (h *SettingHandler) GetGatewayRoutingSettings(c *gin.Context) {
	settings, err := h.settingService.GetGatewayRoutingSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	runtime, err := h.settingService.GetGatewayRoutingRuntime(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, GatewayRoutingAdminResponse{Settings: settings, Runtime: runtime})
}

// UpdateGatewayRoutingSettings persists target weights without overwriting runtime traffic state.
func (h *SettingHandler) UpdateGatewayRoutingSettings(c *gin.Context) {
	var settings service.GatewayRoutingSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.settingService.SetGatewayRoutingSettings(c.Request.Context(), &settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	runtime, err := h.settingService.GetGatewayRoutingRuntime(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, GatewayRoutingAdminResponse{Settings: &settings, Runtime: runtime})
}
