package routes

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine, cfg *config.Config, db *sql.DB, redisClient *redis.Client) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/health/live", func(c *gin.Context) {
		role := config.InstanceRoleControl
		instanceID := ""
		if cfg != nil {
			role = config.NormalizeInstanceRole(cfg.InstanceRole)
			instanceID = cfg.InstanceID
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "role": role, "instance_id": instanceID})
	})
	r.GET("/health/ready", func(c *gin.Context) {
		role := config.InstanceRoleControl
		instanceID := ""
		if cfg != nil {
			role = config.NormalizeInstanceRole(cfg.InstanceRole)
			instanceID = cfg.InstanceID
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		checks := gin.H{"database": "ok", "redis": "ok"}
		ready := true
		if db == nil || db.PingContext(ctx) != nil {
			checks["database"] = "unavailable"
			ready = false
		}
		if redisClient == nil || redisClient.Ping(ctx).Err() != nil {
			checks["redis"] = "unavailable"
			ready = false
		}
		statusCode := http.StatusOK
		status := "ready"
		if !ready {
			statusCode = http.StatusServiceUnavailable
			status = "not_ready"
		}
		c.JSON(statusCode, gin.H{"status": status, "role": role, "instance_id": instanceID, "checks": checks})
	})
	if cfg != nil && cfg.IsGateway() {
		return
	}

	// Claude Code 遥测日志（忽略，直接返回200）
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})
}
