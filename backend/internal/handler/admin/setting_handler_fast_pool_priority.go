package admin

import (
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
		Mode service.FastPoolPriorityMode `json:"mode" binding:"required,oneof=intelligence rate"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "mode must be intelligence or rate")
		return
	}
	settings, err := h.settingService.UpdateFastPoolPrioritySettings(c.Request.Context(), req.Mode)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
