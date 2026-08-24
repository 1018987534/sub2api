package handler

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ratelimit "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	supportChatMessagesPerMinute = 10
	supportChatRateLimitWindow   = time.Minute
	supportChatMaxAttachment     = 4 << 20
	supportChatMaxRequest        = supportChatMaxAttachment + 256<<10
	supportChatMaxJSONRequest    = 64 << 10
)

var supportChatAttachmentTypes = map[string]struct{}{
	"image/gif":       {},
	"image/jpeg":      {},
	"image/png":       {},
	"image/webp":      {},
	"text/plain":      {},
	"application/pdf": {},
}

const (
	supportChatDOCType  = "application/msword"
	supportChatDOCXType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
)

var supportChatOLEHeader = []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}

type SupportChatHandler struct {
	db      *sql.DB
	limiter *ratelimit.RateLimiter
}

func NewSupportChatHandler(db *sql.DB, redisClient *redis.Client) *SupportChatHandler {
	var limiter *ratelimit.RateLimiter
	if redisClient != nil {
		limiter = ratelimit.NewRateLimiter(redisClient)
	}
	return &SupportChatHandler{db: db, limiter: limiter}
}

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
	ID             int64              `json:"id"`
	ConversationID int64              `json:"conversation_id"`
	SenderType     string             `json:"sender_type"`
	SenderID       int64              `json:"sender_id"`
	Content        string             `json:"content"`
	Kind           string             `json:"kind"`
	CreatedAt      time.Time          `json:"created_at"`
	RecalledAt     *time.Time         `json:"recalled_at,omitempty"`
	Attachment     *supportAttachment `json:"attachment,omitempty"`
}
type supportMessageInput struct {
	Content        string `json:"content"`
	Kind           string `json:"kind"`
	IdempotencyKey string `json:"idempotency_key"`
}

type supportAttachment struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type supportAttachmentUpload struct {
	Filename    string
	ContentType string
	Data        []byte
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
	rows, err := h.db.QueryContext(c, `SELECT m.id,m.conversation_id,m.sender_type,m.sender_id,m.content,m.kind,m.created_at,m.recalled_at,a.id,a.filename,a.content_type,a.size_bytes FROM support_messages m LEFT JOIN support_attachments a ON a.message_id=m.id WHERE m.conversation_id=$1 ORDER BY m.created_at ASC,m.id ASC LIMIT $2`, id, limit)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	defer rows.Close()
	items := make([]supportMessage, 0, limit)
	for rows.Next() {
		var m supportMessage
		var attachmentID sql.NullInt64
		var filename, contentType sql.NullString
		var sizeBytes sql.NullInt64
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &m.Kind, &m.CreatedAt, &m.RecalledAt, &attachmentID, &filename, &contentType, &sizeBytes); err != nil {
			response.Error(c, 500, err.Error())
			return
		}
		if attachmentID.Valid {
			m.Attachment = &supportAttachment{ID: attachmentID.Int64, Filename: filename.String, ContentType: contentType.String, SizeBytes: sizeBytes.Int64}
		}
		items = append(items, m)
	}
	response.Success(c, gin.H{"items": items, "total": len(items), "page": 1, "page_size": limit, "pages": 1})
}

func (h *SupportChatHandler) insertMessage(c *gin.Context, conversationID, senderID int64, senderType string, in supportMessageInput, uploads ...*supportAttachmentUpload) (supportMessage, error) {
	in.Content = strings.TrimSpace(in.Content)
	var upload *supportAttachmentUpload
	if len(uploads) > 0 {
		upload = uploads[0]
	}
	if (in.Content == "" && upload == nil) || len(in.Content) > 10000 {
		return supportMessage{}, fmt.Errorf("message content must be between 1 and 10000 characters")
	}
	if upload != nil {
		return h.insertMessageWithAttachment(c, conversationID, senderID, senderType, in, upload)
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

func (h *SupportChatHandler) insertMessageWithAttachment(c *gin.Context, conversationID, senderID int64, senderType string, in supportMessageInput, upload *supportAttachmentUpload) (supportMessage, error) {
	if upload == nil || len(upload.Data) == 0 {
		return supportMessage{}, fmt.Errorf("attachment is empty")
	}
	tx, err := h.db.BeginTx(c, nil)
	if err != nil {
		return supportMessage{}, err
	}
	defer tx.Rollback()
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = fmt.Sprintf("%s-%d-%d", senderType, senderID, time.Now().UnixNano())
	}
	var m supportMessage
	err = tx.QueryRowContext(c, `INSERT INTO support_messages (conversation_id,sender_type,sender_id,content,kind,idempotency_key) VALUES ($1,$2,$3,$4,'file',$5) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id,conversation_id,sender_type,sender_id,content,kind,created_at,recalled_at`, conversationID, senderType, senderID, in.Content, in.IdempotencyKey).Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &m.Kind, &m.CreatedAt, &m.RecalledAt)
	if err == sql.ErrNoRows {
		err = tx.QueryRowContext(c, `SELECT id,conversation_id,sender_type,sender_id,content,kind,created_at,recalled_at FROM support_messages WHERE idempotency_key=$1 AND conversation_id=$2 AND sender_type=$3 AND sender_id=$4`, in.IdempotencyKey, conversationID, senderType, senderID).Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &m.Kind, &m.CreatedAt, &m.RecalledAt)
		if err != nil {
			return m, err
		}
		if err := tx.Commit(); err != nil {
			return m, err
		}
		m.Attachment = h.attachmentMetadata(c, m.ID)
		return m, nil
	}
	if err != nil {
		return m, err
	}
	var attachmentID int64
	err = tx.QueryRowContext(c, `INSERT INTO support_attachments (message_id,conversation_id,uploader_type,uploader_id,filename,content_type,size_bytes,data) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, m.ID, conversationID, senderType, senderID, upload.Filename, upload.ContentType, len(upload.Data), upload.Data).Scan(&attachmentID)
	if err != nil {
		return m, err
	}
	column := "unread_by_user"
	if senderType == "user" {
		column = "unread_by_admin"
	}
	if _, err = tx.ExecContext(c, fmt.Sprintf(`UPDATE support_conversations SET %s=%s+1,last_message_at=$2,updated_at=$2 WHERE id=$1`, column, column), conversationID, m.CreatedAt); err != nil {
		return m, err
	}
	if err = tx.Commit(); err != nil {
		return m, err
	}
	m.Attachment = &supportAttachment{ID: attachmentID, Filename: upload.Filename, ContentType: upload.ContentType, SizeBytes: int64(len(upload.Data))}
	return m, nil
}

func (h *SupportChatHandler) attachmentMetadata(c *gin.Context, messageID int64) *supportAttachment {
	var a supportAttachment
	if err := h.db.QueryRowContext(c, `SELECT id,filename,content_type,size_bytes FROM support_attachments WHERE message_id=$1`, messageID).Scan(&a.ID, &a.Filename, &a.ContentType, &a.SizeBytes); err != nil {
		return nil
	}
	return &a
}

func (h *SupportChatHandler) checkMessageRateLimit(c *gin.Context, subjectID int64, senderType string) (bool, time.Duration) {
	if h.limiter == nil || subjectID <= 0 {
		return true, 0
	}
	result, err := h.limiter.Allow(c.Request.Context(), fmt.Sprintf("support-chat:%s:%d", senderType, subjectID), supportChatMessagesPerMinute, supportChatRateLimitWindow)
	if err != nil {
		return false, supportChatRateLimitWindow
	}
	return result.Allowed, result.RetryAfter
}

func rejectMessageRateLimit(c *gin.Context, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = supportChatRateLimitWindow
	}
	seconds := int64(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		seconds++
	}
	c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	response.Error(c, http.StatusTooManyRequests, "Too many messages, please try again later")
}

func parseSupportMessageRequest(c *gin.Context) (supportMessageInput, *supportAttachmentUpload, error) {
	if strings.HasPrefix(strings.ToLower(c.ContentType()), "multipart/form-data") {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, supportChatMaxRequest)
		if err := c.Request.ParseMultipartForm(supportChatMaxRequest); err != nil {
			return supportMessageInput{}, nil, fmt.Errorf("request is too large")
		}
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			return supportMessageInput{}, nil, fmt.Errorf("file is required")
		}
		defer file.Close()
		if header.Size > supportChatMaxAttachment {
			return supportMessageInput{}, nil, fmt.Errorf("file must be 4 MiB or smaller")
		}
		data, err := io.ReadAll(io.LimitReader(file, supportChatMaxAttachment+1))
		if err != nil || len(data) == 0 || len(data) > supportChatMaxAttachment {
			return supportMessageInput{}, nil, fmt.Errorf("file must be 4 MiB or smaller")
		}
		filename := filepath.Base(strings.TrimSpace(strings.ReplaceAll(header.Filename, `\`, "/")))
		filename = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, filename)
		if filename == "" || filename == "." {
			filename = "attachment"
		}
		if len(filename) > 255 {
			filename = filename[:255]
		}
		contentType, ok := detectSupportAttachmentType(filename, data)
		if !ok {
			return supportMessageInput{}, nil, fmt.Errorf("unsupported file type")
		}
		return supportMessageInput{Content: strings.TrimSpace(c.PostForm("content")), IdempotencyKey: c.GetHeader("Idempotency-Key")}, &supportAttachmentUpload{Filename: filename, ContentType: contentType, Data: data}, nil
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, supportChatMaxJSONRequest)
	var in supportMessageInput
	if err := c.ShouldBindJSON(&in); err != nil {
		return in, nil, err
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	return in, nil, nil
}

func detectSupportAttachmentType(filename string, data []byte) (string, bool) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".doc":
		if bytes.HasPrefix(data, supportChatOLEHeader) {
			return supportChatDOCType, true
		}
		return "", false
	case ".docx":
		if isDOCX(data) {
			return supportChatDOCXType, true
		}
		return "", false
	}

	contentType := http.DetectContentType(data)
	if mediaType, _, ok := strings.Cut(contentType, ";"); ok {
		contentType = strings.TrimSpace(mediaType)
	}
	_, ok := supportChatAttachmentTypes[contentType]
	return contentType, ok
}

func isDOCX(data []byte) bool {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	hasContentTypes := false
	hasDocument := false
	for _, file := range reader.File {
		switch file.Name {
		case "[Content_Types].xml":
			hasContentTypes = true
		case "word/document.xml":
			hasDocument = true
		}
	}
	return hasContentTypes && hasDocument
}

func (h *SupportChatHandler) Attachment(c *gin.Context)      { h.serveAttachment(c, false) }
func (h *SupportChatHandler) AdminAttachment(c *gin.Context) { h.serveAttachment(c, true) }

func (h *SupportChatHandler) serveAttachment(c *gin.Context, admin bool) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id <= 0 {
		response.BadRequest(c, "Invalid attachment ID")
		return
	}
	query := `SELECT a.filename,a.content_type,a.data FROM support_attachments a JOIN support_conversations c ON c.id=a.conversation_id WHERE a.id=$1`
	args := []any{id}
	if !admin {
		s, ok := subject(c)
		if !ok {
			response.Unauthorized(c, "User not authenticated")
			return
		}
		query += ` AND c.user_id=$2`
		args = append(args, s.UserID)
	}
	var filename, contentType string
	var data []byte
	if err := h.db.QueryRowContext(c, query, args...).Scan(&filename, &contentType, &data); err == sql.ErrNoRows {
		response.NotFound(c, "Attachment not found")
		return
	} else if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(filename, `"`, "")))
	c.Data(http.StatusOK, contentType, data)
}
func (h *SupportChatHandler) Send(c *gin.Context) {
	s, ok := subject(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if allowed, retryAfter := h.checkMessageRateLimit(c, s.UserID, "user"); !allowed {
		rejectMessageRateLimit(c, retryAfter)
		return
	}
	v, err := h.ensureConversation(c, s.UserID)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	in, upload, err := parseSupportMessageRequest(c)
	if err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	m, err := h.insertMessage(c, v.ID, s.UserID, "user", in, upload)
	if err != nil {
		response.BadRequest(c, err.Error())
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
	if allowed, retryAfter := h.checkMessageRateLimit(c, s.UserID, "admin"); !allowed {
		rejectMessageRateLimit(c, retryAfter)
		return
	}
	in, upload, err := parseSupportMessageRequest(c)
	if err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	m, err := h.insertMessage(c, id, s.UserID, "admin", in, upload)
	if err != nil {
		response.BadRequest(c, err.Error())
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
