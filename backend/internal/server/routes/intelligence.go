package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Admin config can be prepared while monitoring is off; only actual runs need V2.
func registerIntelligenceRoutes(admin *gin.RouterGroup, h *handler.Handlers, settings *service.SettingService) {
	checks := admin.Group("/intelligence-checks")
	checks.GET("/config", h.Intelligence.Configs)
	checks.GET("/:groupID/keys", h.Intelligence.KeyOptions)
	checks.PUT("/:groupID/config", h.Intelligence.Save)
	checks.GET("/:groupID/history", h.Intelligence.History)
	checks.POST("/:groupID/run", channelMonitorModeV2Guard(settings), h.Intelligence.Run)
}
