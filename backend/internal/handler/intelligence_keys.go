package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Explicit allowlist: this picker must never serialize service.APIKey or its credential.
type intelligenceKeyOption struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	GroupID           int64      `json:"group_id"`
	Available         bool       `json:"available"`
	UnavailableReason string     `json:"unavailable_reason,omitempty"`
	QuotaRemaining    float64    `json:"quota_remaining"`
	ExpiresAt         *time.Time `json:"expires_at"`
}

func intelligenceKeyChoice(key service.APIKey, group *service.Group) intelligenceKeyOption {
	option := intelligenceKeyOption{ID: key.ID, Name: key.Name, GroupID: group.ID, Available: true, QuotaRemaining: key.GetQuotaRemaining(), ExpiresAt: key.ExpiresAt}
	switch {
	case group.Status != service.StatusActive:
		option.UnavailableReason = "分组已停用"
	case key.Key == "":
		option.UnavailableReason = "Key 不可用"
	case !key.IsActive():
		option.UnavailableReason = "Key 已停用或不可用"
	case key.IsExpired():
		option.UnavailableReason = "Key 已过期"
	case key.IsQuotaExhausted():
		option.UnavailableReason = "Key 额度已用完"
	}
	option.Available = option.UnavailableReason == ""
	return option
}

// KeyOptions only exposes keys owned by the authenticated administrator and
// already bound to this group. Admin API credentials and other owners' keys
// are intentionally not supported.
func (h *IntelligenceHandler) KeyOptions(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "unauthorized")
		return
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || role != service.RoleAdmin {
		response.Forbidden(c, "administrator required")
		return
	}
	groupID, ok := intelligenceGroupID(c)
	if !ok {
		return
	}
	group, err := h.groups.GetByID(c.Request.Context(), groupID)
	if err != nil || group == nil {
		response.NotFound(c, "group not found")
		return
	}
	page := 1
	if raw := c.Query("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100000 {
			response.BadRequest(c, "invalid page")
			return
		}
		page = value
	}
	search := strings.TrimSpace(c.Query("search"))
	if utf8.RuneCountInString(search) > 100 {
		response.BadRequest(c, "search too long")
		return
	}
	const pageSize = 50
	keys, result, err := h.keys.ListByUserID(c.Request.Context(), subject.UserID, pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: "id", SortOrder: "desc"}, service.APIKeyListFilters{GroupID: &groupID, Search: search})
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "无法读取当前管理员的 API Key")
		return
	}
	options := make([]intelligenceKeyOption, 0, len(keys))
	for _, key := range keys {
		// Defense in depth, even if a repository implementation ignores its filters.
		if key.UserID != subject.UserID || key.GroupID == nil || *key.GroupID != groupID {
			continue
		}
		options = append(options, intelligenceKeyChoice(key, group))
	}
	more := result != nil && int64(page*pageSize) < result.Total
	response.Success(c, gin.H{"items": options, "page": page, "has_more": more})
}
