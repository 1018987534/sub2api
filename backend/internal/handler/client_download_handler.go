package handler

import (
	"database/sql"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/clientdownload"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const downloadTicketCookie = "__Secure-client-download-ticket"

type ClientDownloadHandler struct{ service *clientdownload.Service }

func NewClientDownloadHandler(db *sql.DB) *ClientDownloadHandler {
	return &ClientDownloadHandler{clientdownload.NewService(db, clientdownload.NewPublishedManifestLoader())}
}
func (h *ClientDownloadHandler) Issue(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录后再下载。")
		return
	}
	var req struct {
		ID string `json:"id" binding:"required"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "请选择需要下载的安装包。")
		return
	}
	token, err := h.service.Issue(c.Request.Context(), subject.UserID, req.ID)
	if err != nil {
		if errors.Is(err, clientdownload.ErrLimited) {
			c.Header("Retry-After", "3600")
			response.Error(c, http.StatusTooManyRequests, "本站下载次数或流量额度已达上限，请稍后再试，或使用官方下载。")
			return
		}
		response.Error(c, http.StatusServiceUnavailable, "本站下载暂不可用，请稍后再试，或使用官方下载。")
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(downloadTicketCookie, token, int(clientdownload.TicketLifetime.Seconds()), "/downloads/clients/", "xiaohondou.com", true, true)
	response.Success(c, gin.H{"url": "https://downloads.xiaohondou.com/downloads/clients/fetch"})
}
func (h *ClientDownloadHandler) Transfer(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	// This host's Nginx always overwrites X-Real-IP from the TCP peer and never
	// routes arbitrary /api paths. Other application vhosts cannot invoke it.
	if c.Request.Host != "downloads.xiaohondou.com" {
		c.Status(http.StatusForbidden)
		return
	}
	if c.Request.Method != http.MethodGet {
		c.Header("Allow", "GET")
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	token, err := c.Cookie(downloadTicketCookie)
	if err != nil {
		c.String(http.StatusForbidden, "请返回小白教程，登录后重新点击下载。")
		return
	}
	f, err := h.service.Consume(c.Request.Context(), token, c.GetHeader("X-Real-IP"))
	if err != nil {
		if errors.Is(err, clientdownload.ErrLimited) {
			c.Header("Retry-After", "3600")
			c.String(http.StatusTooManyRequests, "本站下载次数或流量额度已达上限，请稍后再试，或使用官方下载。")
			return
		}
		status := http.StatusServiceUnavailable
		if errors.Is(err, clientdownload.ErrInvalidTicket) {
			status = http.StatusForbidden
		}
		c.String(status, "下载链接已失效或暂不可用，请返回小白教程重新点击下载，或使用官方下载。")
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(downloadTicketCookie, "", -1, "/downloads/clients/", "xiaohondou.com", true, true)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Accel-Redirect", "/_protected/client-downloads/"+strings.TrimPrefix(f.URL, "/downloads/clients/"))
	c.Status(http.StatusOK)
}
