package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type LotteryHandler struct {
	service *service.LotteryService
}

func NewLotteryHandler(lotteryService *service.LotteryService) *LotteryHandler {
	return &LotteryHandler{service: lotteryService}
}

func (h *LotteryHandler) Current(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	current, err := h.service.GetCurrent(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, current)
}

// Announcement serves the redacted payload used by the public snapshot page
// and external notification integrations. It omits user-specific fields.
func (h *LotteryHandler) Announcement(c *gin.Context) {
	announcement, err := h.service.GetAnnouncement(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, announcement)
}

func (h *LotteryHandler) Join(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	result, err := h.service.Join(c.Request.Context(), subject.UserID, ip.GetClientIP(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *LotteryHandler) Rounds(c *gin.Context) {
	page, pageSize := lotteryPagination(c)
	result, err := h.service.ListPublicRounds(c.Request.Context(), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *LotteryHandler) AdminConfig(c *gin.Context) {
	cfg, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *LotteryHandler) AdminUpdateConfig(c *gin.Context) {
	var cfg service.LotteryConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	updated, err := h.service.UpdateConfig(c.Request.Context(), cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}

func (h *LotteryHandler) AdminStartRound(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Admin not authenticated")
		return
	}
	round, err := h.service.StartRound(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, round)
}

func (h *LotteryHandler) AdminDraw(c *gin.Context) {
	roundID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || roundID <= 0 {
		response.BadRequest(c, "Invalid round ID")
		return
	}
	result, err := h.service.DrawRound(c.Request.Context(), roundID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *LotteryHandler) AdminRounds(c *gin.Context) {
	page, pageSize := lotteryPagination(c)
	result, err := h.service.ListRounds(c.Request.Context(), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func lotteryPagination(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "8"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 8
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
