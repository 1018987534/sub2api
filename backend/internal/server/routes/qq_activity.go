package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func RegisterQQActivityRoutes(v1 *gin.RouterGroup, h *handler.QQActivityHandler, jwt middleware.JWTAuthMiddleware, admin middleware.AdminAuthMiddleware, audit middleware.AuditLogMiddleware, settings *service.SettingService, limits *middleware.PanelRateLimiter) {
	if h == nil {
		return
	}
	v1.POST("/qq-activity/bridge", h.Bridge)
	user := v1.Group("/qq-activity", gin.HandlerFunc(jwt), middleware.BackendModeUserGuard(settings), limits.Global(), gin.HandlerFunc(audit))
	user.GET("/current", h.Current)
	user.GET("/binding", h.Binding)
	user.POST("/binding/send-code", h.SendBindingCode)
	user.POST("/binding/verify", h.VerifyBindingEmail)
	user.POST("/binding/unbind", h.Unbind)
	user.POST("/claim", h.Claim)
	management := v1.Group("/admin/qq-activity", gin.HandlerFunc(admin), gin.HandlerFunc(audit), limits.Global(), middleware.AdminComplianceGuard(settings))
	management.GET("", h.AdminCurrent)
	management.POST("/control", h.AdminControl)
}
