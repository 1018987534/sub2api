package routes

import (
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func RegisterClientDownloadRoutes(v1 *gin.RouterGroup, db *sql.DB, jwt middleware.JWTAuthMiddleware, settings *service.SettingService, limits *middleware.PanelRateLimiter) {
	h := handler.NewClientDownloadHandler(db)
	v1.POST("/client-downloads/tickets", gin.HandlerFunc(jwt), middleware.BackendModeUserGuard(settings), limits.Global(), h.Issue)
	// Only a short-lived one-use cookie authorizes the dedicated Nginx host.
	v1.GET("/client-downloads/transfer", h.Transfer)
	v1.HEAD("/client-downloads/transfer", h.Transfer)
}
