package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type FeedbackHandler struct {
	db *sql.DB
}

func NewFeedbackHandler(db *sql.DB) *FeedbackHandler {
	return &FeedbackHandler{db: db}
}

type feedbackSubmission struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	Category     string    `json:"category"`
	Subject      string    `json:"subject"`
	Content      string    `json:"content"`
	CreatedAt    time.Time `json:"created_at"`
	UserEmail    string    `json:"user_email,omitempty"`
	UserUsername string    `json:"user_username,omitempty"`
}

type feedbackInput struct {
	Category string `json:"category"`
	Subject  string `json:"subject"`
	Content  string `json:"content"`
}

const feedbackColumns = `id,user_id,category,subject,content,created_at`

func (h *FeedbackHandler) Create(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var in feedbackInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid feedback request")
		return
	}
	in.Subject = strings.TrimSpace(in.Subject)
	in.Content = strings.TrimSpace(in.Content)
	if in.Category != "problem" && in.Category != "suggestion" && in.Category != "other" {
		response.BadRequest(c, "Invalid feedback category")
		return
	}
	if n := utf8.RuneCountInString(in.Subject); n < 1 || n > 100 {
		response.BadRequest(c, "Feedback subject must be between 1 and 100 characters")
		return
	}
	if n := utf8.RuneCountInString(in.Content); n < 1 || n > 5000 {
		response.BadRequest(c, "Feedback content must be between 1 and 5000 characters")
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(key) == 0 || len(key) > 128 {
		response.BadRequest(c, "An Idempotency-Key of at most 128 bytes is required")
		return
	}
	if h.db == nil {
		response.InternalError(c, "Feedback storage is unavailable")
		return
	}
	var out feedbackSubmission
	err := h.db.QueryRowContext(c.Request.Context(), `INSERT INTO feedback_submissions (user_id,category,subject,content,idempotency_key) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (user_id,idempotency_key) DO NOTHING RETURNING `+feedbackColumns,
		subject.UserID, in.Category, in.Subject, in.Content, key).Scan(&out.ID, &out.UserID, &out.Category, &out.Subject, &out.Content, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = h.db.QueryRowContext(c.Request.Context(), `SELECT `+feedbackColumns+` FROM feedback_submissions WHERE user_id=$1 AND idempotency_key=$2`, subject.UserID, key).
			Scan(&out.ID, &out.UserID, &out.Category, &out.Subject, &out.Content, &out.CreatedAt)
		if err == nil {
			if out.Category != in.Category || out.Subject != in.Subject || out.Content != in.Content {
				response.Error(c, http.StatusConflict, "This feedback key was already used for another submission")
				return
			}
			response.Success(c, out)
			return
		}
	}
	if err != nil {
		response.InternalError(c, "Failed to submit feedback")
		return
	}
	response.Created(c, out)
}

// AdminList is the only administrator feedback operation: there are no replies,
// status updates or mutations, and feedback never enters a support conversation.
func (h *FeedbackHandler) AdminList(c *gin.Context) {
	subject, authenticated := middleware.GetAuthSubjectFromContext(c)
	role, _ := middleware.GetUserRoleFromContext(c)
	if !authenticated || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if role != service.RoleAdmin {
		response.Forbidden(c, "Administrator access required")
		return
	}
	if h.db == nil {
		response.InternalError(c, "Feedback storage is unavailable")
		return
	}
	page, pageSize := response.ParsePagination(c)
	var total int64
	if err := h.db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM feedback_submissions`).Scan(&total); err != nil {
		response.InternalError(c, "Failed to load feedback")
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT f.id,f.user_id,f.category,f.subject,f.content,f.created_at,COALESCE(u.email,''),COALESCE(u.username,'') FROM feedback_submissions f LEFT JOIN users u ON u.id=f.user_id ORDER BY f.created_at DESC,f.id DESC LIMIT $1 OFFSET $2`, pageSize, (page-1)*pageSize)
	if err != nil {
		response.InternalError(c, "Failed to load feedback")
		return
	}
	defer func() { _ = rows.Close() }()
	items := make([]feedbackSubmission, 0, pageSize)
	for rows.Next() {
		var item feedbackSubmission
		if err := rows.Scan(&item.ID, &item.UserID, &item.Category, &item.Subject, &item.Content, &item.CreatedAt, &item.UserEmail, &item.UserUsername); err != nil {
			response.InternalError(c, "Failed to load feedback")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		response.InternalError(c, "Failed to load feedback")
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}
