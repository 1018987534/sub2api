package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

type SupportChatHandler struct{ db *sql.DB }

func NewSupportChatHandler(db *sql.DB) *SupportChatHandler { return &SupportChatHandler{db: db} }

type supportConversation struct {
	ID                    int64      `json:"id"`
	UserID                int64      `json:"user_id"`
	UserEmail             string     `json:"user_email,omitempty"`
	UserUsername          string     `json:"user_username,omitempty"`
	UnreadByUser          int        `json:"unread_by_user"`
	UnreadByAdmin         int        `json:"unread_by_admin"`
	ManuallyUnreadByAdmin bool       `json:"manually_unread_by_admin"`
	LastMessageAt         *time.Time `json:"last_message_at,omitempty"`
	UpdatedAt             time.Time  `json:"updated_at"`
}
type supportMessage struct {
	ID             int64      `json:"id"`
	ConversationID int64      `json:"conversation_id"`
	SenderType     string     `json:"sender_type"`
	SenderID       int64      `json:"sender_id"`
	Content        string     `json:"content"`
	Kind           string     `json:"kind"`
	CreatedAt      time.Time  `json:"created_at"`
	RecalledAt     *time.Time `json:"recalled_at,omitempty"`
}
type supportMessageInput struct {
	Content        string `json:"content"`
	Kind           string `json:"kind"`
	IdempotencyKey string `json:"idempotency_key"`
}

func subject(c *gin.Context) (middleware2.AuthSubject, bool) {
	return middleware2.GetAuthSubjectFromContext(c)
}

func (h *SupportChatHandler) ensureConversation(c *gin.Context, userID int64) (supportConversation, error) {
	if h.db == nil {
		return supportConversation{}, fmt.Errorf("support chat database is unavailable")
	}
	if _, err := h.db.ExecContext(c, `INSERT INTO support_conversations (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return supportConversation{}, err
	}
	var out supportConversation
	err := h.db.QueryRowContext(c, `SELECT id,user_id,unread_by_user,unread_by_admin,manually_unread_by_admin,last_message_at,updated_at FROM support_conversations WHERE user_id=$1`, userID).Scan(&out.ID, &out.UserID, &out.UnreadByUser, &out.UnreadByAdmin, &out.ManuallyUnreadByAdmin, &out.LastMessageAt, &out.UpdatedAt)
	return out, err
}

func (h *SupportChatHandler) Conversation(c *gin.Context) {
	s, ok := subject(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	out, err := h.ensureConversation(c, s.UserID)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, out)
}
func (h *SupportChatHandler) Messages(c *gin.Context) {
	s, ok := subject(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	v, err := h.ensureConversation(c, s.UserID)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	h.listMessages(c, v.ID)
}
func (h *SupportChatHandler) listMessages(c *gin.Context, id int64) {
	limit := 100
	if n, e := strconv.Atoi(c.Query("page_size")); e == nil && n > 0 && n <= 200 {
		limit = n
	}
	rows, err := h.db.QueryContext(c, `SELECT id,conversation_id,sender_type,sender_id,content,kind,created_at,recalled_at FROM support_messages WHERE conversation_id=$1 ORDER BY created_at ASC,id ASC LIMIT $2`, id, limit)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	defer rows.Close()
	items := make([]supportMessage, 0, limit)
	for rows.Next() {
		var m supportMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &m.Kind, &m.CreatedAt, &m.RecalledAt); err != nil {
			response.Error(c, 500, err.Error())
			return
		}
		items = append(items, m)
	}
	response.Success(c, gin.H{"items": items, "total": len(items), "page": 1, "page_size": limit, "pages": 1})
}

func (h *SupportChatHandler) insertMessage(c *gin.Context, conversationID, senderID int64, senderType string, in supportMessageInput) (supportMessage, error) {
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" || len(in.Content) > 10000 {
		return supportMessage{}, fmt.Errorf("message content must be between 1 and 10000 characters")
	}
	if in.Kind == "" {
		in.Kind = "text"
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = fmt.Sprintf("%s-%d-%d", senderType, senderID, time.Now().UnixNano())
	}
	var m supportMessage
	err := h.db.QueryRowContext(c, `INSERT INTO support_messages (conversation_id,sender_type,sender_id,content,kind,idempotency_key) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id,conversation_id,sender_type,sender_id,content,kind,created_at,recalled_at`, conversationID, senderType, senderID, in.Content, in.Kind, in.IdempotencyKey).Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &m.Kind, &m.CreatedAt, &m.RecalledAt)
	isNew := true
	if err == sql.ErrNoRows {
		// A retried request returns the original message and must not increment
		// the unread counter again. Keep the key scoped to its original sender
		// and conversation so a key cannot replay another conversation's message.
		err = h.db.QueryRowContext(c, `SELECT id,conversation_id,sender_type,sender_id,content,kind,created_at,recalled_at FROM support_messages WHERE idempotency_key=$1 AND conversation_id=$2 AND sender_type=$3 AND sender_id=$4`, in.IdempotencyKey, conversationID, senderType, senderID).Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &m.Kind, &m.CreatedAt, &m.RecalledAt)
		if err != nil {
			return m, err
		}
		isNew = false
	} else if err != nil {
		return m, err
	}
	if !isNew {
		return m, nil
	}
	column := "unread_by_user"
	if senderType == "user" {
		column = "unread_by_admin"
	}
	_, err = h.db.ExecContext(c, fmt.Sprintf(`UPDATE support_conversations SET %s=%s+1,last_message_at=$2,updated_at=$2 WHERE id=$1`, column, column), conversationID, m.CreatedAt)
	return m, err
}
func (h *SupportChatHandler) Send(c *gin.Context) {
	s, ok := subject(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	v, err := h.ensureConversation(c, s.UserID)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	var in supportMessageInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	m, err := h.insertMessage(c, v.ID, s.UserID, "user", in)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Created(c, m)
}
func (h *SupportChatHandler) Read(c *gin.Context) {
	s, ok := subject(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	_, err := h.db.ExecContext(c, `UPDATE support_conversations SET unread_by_user=0,updated_at=NOW() WHERE user_id=$1`, s.UserID)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "ok"})
}
func (h *SupportChatHandler) UnreadCount(c *gin.Context) {
	s, ok := subject(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var n int
	err := h.db.QueryRowContext(c, `SELECT unread_by_user FROM support_conversations WHERE user_id=$1`, s.UserID).Scan(&n)
	if err == sql.ErrNoRows {
		n = 0
		err = nil
	}
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"unread_count": n})
}

func (h *SupportChatHandler) AdminConversations(c *gin.Context) {
	search := strings.TrimSpace(c.Query("search"))
	unread := c.Query("unread_only") == "1" || strings.EqualFold(c.Query("unread_only"), "true")
	rows, err := h.db.QueryContext(c, `SELECT c.id,c.user_id,u.email,u.username,c.unread_by_user,c.unread_by_admin,c.manually_unread_by_admin,c.last_message_at,c.updated_at FROM support_conversations c JOIN users u ON u.id=c.user_id WHERE ($1='' OR u.email ILIKE '%'||$1||'%' OR u.username ILIKE '%'||$1||'%') AND ($2=false OR c.unread_by_admin>0 OR c.manually_unread_by_admin) ORDER BY COALESCE(c.last_message_at,c.updated_at) DESC,c.id DESC LIMIT 100`, search, unread)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	defer rows.Close()
	items := make([]supportConversation, 0, 100)
	for rows.Next() {
		var v supportConversation
		if err := rows.Scan(&v.ID, &v.UserID, &v.UserEmail, &v.UserUsername, &v.UnreadByUser, &v.UnreadByAdmin, &v.ManuallyUnreadByAdmin, &v.LastMessageAt, &v.UpdatedAt); err != nil {
			response.Error(c, 500, err.Error())
			return
		}
		items = append(items, v)
	}
	response.Success(c, gin.H{"items": items, "total": len(items), "page": 1, "page_size": 100, "pages": 1})
}
func (h *SupportChatHandler) AdminMessages(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id <= 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}
	h.listMessages(c, id)
}
func (h *SupportChatHandler) AdminSend(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id <= 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}
	s, ok := subject(c)
	if !ok {
		response.Unauthorized(c, "Admin not authenticated")
		return
	}
	var in supportMessageInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	m, err := h.insertMessage(c, id, s.UserID, "admin", in)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Created(c, m)
}
func (h *SupportChatHandler) AdminRead(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	_, err := h.db.ExecContext(c, `UPDATE support_conversations SET unread_by_admin=0,manually_unread_by_admin=false,updated_at=NOW() WHERE id=$1`, id)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "ok"})
}
func (h *SupportChatHandler) AdminUnreadCount(c *gin.Context) {
	var n int
	err := h.db.QueryRowContext(c, `SELECT COALESCE(SUM(unread_by_admin),0) FROM support_conversations`).Scan(&n)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"unread_count": n})
}
