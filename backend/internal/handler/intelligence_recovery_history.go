package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
	"time"
)

func (h *IntelligenceHandler) RecoveryHistory(c *gin.Context) {
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	if role, ok := middleware.GetUserRoleFromContext(c); !ok || role != service.RoleAdmin {
		response.Forbidden(c, "administrator required")
		return
	}
	before := int64(0)
	if raw := c.Query("before_id"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			response.BadRequest(c, "invalid history cursor")
			return
		}
	}
	rows, err := h.recoveryAdmin.RecoveryHistory(c.Request.Context(), before, 25)
	if err != nil {
		response.InternalError(c, "无法读取账号恢复检测记录")
		return
	}
	queue, err := h.recoveryAdmin.RecoveryQueue(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取账号恢复调度")
		return
	}
	more := len(rows) > 24
	if more {
		rows = rows[:24]
	}
	response.Success(c, gin.H{"items": rows, "queue": queue, "has_more": more, "server_time": time.Now().UTC()})
}
