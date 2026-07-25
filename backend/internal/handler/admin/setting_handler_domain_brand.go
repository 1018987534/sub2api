package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GetDomainBrandConfig returns the small per-domain portal configuration.
// GET /api/v1/admin/settings/domain-brand-config
func (h *SettingHandler) GetDomainBrandConfig(c *gin.Context) {
	config, err := h.settingService.GetDomainBrandConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}

// UpdateDomainBrandConfig replaces the complete domain configuration after
// validating every configured group is active and belongs to one domain only.
// PUT /api/v1/admin/settings/domain-brand-config
func (h *SettingHandler) UpdateDomainBrandConfig(c *gin.Context) {
	var req service.DomainBrandConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	config, err := h.settingService.UpdateDomainBrandConfig(c.Request.Context(), &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}
