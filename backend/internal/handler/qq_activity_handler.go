package handler

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/qqactivity"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type QQActivityHandler struct {
	store        *qqactivity.Store
	tokenFile    string
	emailService *service.EmailService
}

func NewQQActivityHandler(db *sql.DB, authCache service.APIKeyAuthCacheInvalidator, billingCache *service.BillingCacheService, cfg *config.Config, emailService *service.EmailService) *QQActivityHandler {
	store := qqactivity.NewStore(db, func(ctx context.Context, id int64) {
		if authCache != nil {
			authCache.InvalidateAuthCacheByUserID(ctx, id)
		}
		if billingCache != nil {
			_ = billingCache.InvalidateUserBalance(ctx, id)
		}
	})
	return &QQActivityHandler{store: store, tokenFile: filepath.Join(cfg.Pricing.DataDir, "qq-activity-bridge.token"), emailService: emailService}
}

func (h *QQActivityHandler) Binding(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录。")
		return
	}
	result, err := h.store.BindingStatus(c.Request.Context(), subject.UserID)
	if err != nil {
		response.Error(c, 503, "绑定状态暂时不可用，请稍后重试。")
		return
	}
	response.Success(c, result)
}

func (h *QQActivityHandler) SendBindingCode(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录。")
		return
	}
	var req struct {
		Email string `json:"email" binding:"required,max=80"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, qqactivity.ErrBindingEmail.Error())
		return
	}
	if h.emailService == nil {
		response.Error(c, 503, "邮件发送暂时不可用，请稍后重试。")
		return
	}
	result, err := h.store.SendBindingCode(c.Request.Context(), subject.UserID, req.Email, middleware2.SecurityClientIP(c), func(ctx context.Context, email, code string) error {
		return h.emailService.SendEmail(ctx, email, "[小红豆] QQ 绑定验证码", fmt.Sprintf("<p>你正在为当前登录的小红豆账号绑定 QQ 邮箱。</p><p>验证码：<strong>%s</strong></p><p>10 分钟内有效。请回到官网右上角「绑定 QQ」填写，不要把验证码发到 QQ 群或交给他人。若非本人操作，请忽略本邮件。</p>", code))
	})
	if err != nil {
		bindingError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *QQActivityHandler) VerifyBindingEmail(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录。")
		return
	}
	var req struct {
		Email     string `json:"email" binding:"required,max=80"`
		Challenge string `json:"challenge" binding:"required,len=48"`
		Code      string `json:"code" binding:"required,max=32"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, qqactivity.ErrBindingCode.Error())
		return
	}
	result, err := h.store.VerifyBindingEmail(c.Request.Context(), subject.UserID, req.Email, req.Challenge, req.Code)
	if err != nil {
		bindingError(c, err)
		return
	}
	response.Success(c, result)
}

func bindingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, qqactivity.ErrBindingRate):
		c.Header("Retry-After", "60")
		response.Error(c, 429, err.Error())
	case errors.Is(err, qqactivity.ErrBindingEmail), errors.Is(err, qqactivity.ErrBindingCode), errors.Is(err, qqactivity.ErrBindingConflict), errors.Is(err, qqactivity.ErrBindingAlready):
		response.BadRequest(c, err.Error())
	default:
		response.Error(c, 503, "QQ 绑定暂时不可用，请稍后重试。")
	}
}

func (h *QQActivityHandler) Unbind(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录。")
		return
	}
	if err := h.store.Unbind(c.Request.Context(), subject.UserID); err != nil {
		bindingError(c, err)
		return
	}
	response.Success(c, qqactivity.BindingStatus{})
}
func (h *QQActivityHandler) Current(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录。")
		return
	}
	result, err := h.store.UserSummary(c.Request.Context(), subject.UserID)
	if err != nil {
		response.Error(c, 503, "活动暂时不可用，请稍后重试。")
		return
	}
	response.Success(c, result)
}
func (h *QQActivityHandler) Claim(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录。")
		return
	}
	var req struct {
		Token string `json:"claim"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "确认信息无效。")
		return
	}
	result, err := h.store.Claim(c.Request.Context(), subject.UserID, req.Token, c.Query("issue_code") == "true")
	if err != nil {
		response.BadRequest(c, qqactivity.ErrClaim.Error())
		return
	}
	response.Success(c, result)
}
func (h *QQActivityHandler) AdminCurrent(c *gin.Context) {
	result, err := h.store.AdminReport(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "活动暂时不可用，请稍后重试。")
		return
	}
	response.Success(c, result)
}
func (h *QQActivityHandler) AdminControl(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录。")
		return
	}
	var req struct {
		Action         string                     `json:"action"`
		Status         string                     `json:"status"`
		Invitee        string                     `json:"invitee"`
		Inviter        string                     `json:"inviter"`
		GroupID        string                     `json:"group_id"`
		Evidence       string                     `json:"evidence"`
		QQ             string                     `json:"qq"`
		Reason         string                     `json:"reason"`
		Settings       qqactivity.SettingsUpdate  `json:"settings"`
		BindingDisplay *qqactivity.BindingDisplay `json:"binding_display"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "无效的请求。")
		return
	}
	actor := "admin:" + strconv.FormatInt(subject.UserID, 10)
	var err error
	switch req.Action {
	case "status":
		err = h.store.Control(c.Request.Context(), actor, req.Status)
	case "settings":
		err = h.store.UpdateSettings(c.Request.Context(), actor, req.Settings)
	case "binding_display":
		if req.BindingDisplay == nil {
			response.BadRequest(c, "请填写绑定弹窗展示配置。")
			return
		}
		err = h.store.UpdateBindingDisplay(c.Request.Context(), actor, *req.BindingDisplay)
	case "verify_invitation":
		err = h.store.VerifyInvitation(c.Request.Context(), actor, req.Invitee, req.Inviter, req.GroupID, req.Evidence)
	case "lock_binding":
		err = h.store.LockBinding(c.Request.Context(), actor, req.QQ, req.Reason)
	case "retry":
		err = h.store.Tick(c.Request.Context())
	default:
		response.BadRequest(c, "不支持的操作。")
		return
	}
	if err != nil {
		if errors.Is(err, qqactivity.ErrBindingDisplay) {
			response.BadRequest(c, err.Error())
			return
		}
		response.BadRequest(c, "操作未完成，请核对活动状态、绑定记录和邀请证据后重试。")
		return
	}
	response.Success(c, gin.H{"success": true})
}

// Bridge is called by the dedicated daemon. The credential stays in the private
// mounted data directory and is never returned in frontend settings.
func (h *QQActivityHandler) Bridge(c *gin.Context) {
	token, err := os.ReadFile(h.tokenFile)
	expected := strings.TrimSpace(string(token))
	got := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if err != nil || len(expected) < 48 || subtle.ConstantTimeCompare([]byte(expected), []byte(got)) != 1 {
		c.Status(http.StatusNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var req struct {
		Action    string                     `json:"action"`
		Event     qqactivity.Event           `json:"event"`
		Groups    []qqactivity.GroupSnapshot `json:"groups"`
		Activate  bool                       `json:"activate"`
		ID        int64                      `json:"id"`
		Delivered bool                       `json:"delivered"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid bridge payload")
		return
	}
	var result any = gin.H{"ack": true}
	switch req.Action {
	case "initialize":
		err = h.store.Initialize(c.Request.Context(), req.Groups, req.Activate)
	case "event":
		err = h.store.Process(c.Request.Context(), req.Event)
	case "tick":
		err = h.store.Tick(c.Request.Context())
	case "status":
		result, err = h.store.BridgeStatus(c.Request.Context())
	case "outbox":
		result, err = h.store.Outbox(c.Request.Context())
	case "ack":
		err = h.store.Acknowledge(c.Request.Context(), req.ID, req.Delivered)
	case "levels":
		result, err = h.store.LevelChecks(c.Request.Context())
	default:
		response.BadRequest(c, "Invalid bridge action")
		return
	}
	if err != nil {
		response.Error(c, 503, "Activity operation pending; retry without changing the event id")
		return
	}
	response.Success(c, result)
}
