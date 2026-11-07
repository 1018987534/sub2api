package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetFastPoolThresholdSettings(c *gin.Context) {
	settings, err := h.settingService.GetFastPoolThresholdSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateFastPoolThresholdSettings(c *gin.Context) {
	var req struct {
		EnterSeconds int `json:"enter_seconds" binding:"required,min=1,max=86400"`
		ExitSeconds  int `json:"exit_seconds" binding:"required,min=1,max=86400"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "enter_seconds and exit_seconds must be whole seconds from 1 to 86400")
		return
	}
	settings, err := h.settingService.UpdateFastPoolThresholdSettings(c.Request.Context(), req.EnterSeconds, req.ExitSeconds)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
