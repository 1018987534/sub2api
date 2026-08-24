package handler

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func supportChatTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/chat/messages", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
	return c
}

func supportMessageRows(id, conversationID, senderID int64, senderType, content string, createdAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "conversation_id", "sender_type", "sender_id", "content", "kind", "created_at", "recalled_at"}).
		AddRow(id, conversationID, senderType, senderID, content, "text", createdAt, nil)
}

func TestSupportChatInsertMessageRejectsBlankContent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	h := NewSupportChatHandler(db, nil)
	_, err = h.insertMessage(supportChatTestContext(), 41, 7, "user", supportMessageInput{Content: " \n\t"})
	require.EqualError(t, err, "message content must be between 1 and 10000 characters")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSupportChatInsertMessageUpdatesUnreadForEachSender(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	h := NewSupportChatHandler(db, nil)
	now := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	insertPattern := `INSERT INTO support_messages .*ON CONFLICT \(idempotency_key\) DO NOTHING RETURNING`
	updateAdminPattern := `UPDATE support_conversations SET unread_by_admin=unread_by_admin\+1,last_message_at=\$2,updated_at=\$2 WHERE id=\$1`
	updateUserPattern := `UPDATE support_conversations SET unread_by_user=unread_by_user\+1,last_message_at=\$2,updated_at=\$2 WHERE id=\$1`

	mock.ExpectQuery(insertPattern).WithArgs(int64(41), "user", int64(7), "hello", "text", "user-key").WillReturnRows(supportMessageRows(101, 41, 7, "user", "hello", now))
	mock.ExpectExec(updateAdminPattern).WithArgs(int64(41), now).WillReturnResult(sqlmock.NewResult(0, 1))
	m, err := h.insertMessage(supportChatTestContext(), 41, 7, "user", supportMessageInput{Content: "  hello ", Kind: "text", IdempotencyKey: "user-key"})
	require.NoError(t, err)
	require.Equal(t, int64(101), m.ID)

	mock.ExpectQuery(insertPattern).WithArgs(int64(41), "admin", int64(9), "reply", "text", "admin-key").WillReturnRows(supportMessageRows(102, 41, 9, "admin", "reply", now.Add(time.Minute)))
	mock.ExpectExec(updateUserPattern).WithArgs(int64(41), now.Add(time.Minute)).WillReturnResult(sqlmock.NewResult(0, 1))
	m, err = h.insertMessage(supportChatTestContext(), 41, 9, "admin", supportMessageInput{Content: "reply", Kind: "text", IdempotencyKey: "admin-key"})
	require.NoError(t, err)
	require.Equal(t, int64(102), m.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSupportChatInsertMessageIdempotentRetryDoesNotIncrementUnread(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	h := NewSupportChatHandler(db, nil)
	now := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	insertPattern := `INSERT INTO support_messages .*ON CONFLICT \(idempotency_key\) DO NOTHING RETURNING`
	mock.ExpectQuery(insertPattern).WithArgs(int64(41), "user", int64(7), "hello", "text", "retry-key").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,conversation_id,sender_type,sender_id,content,kind,created_at,recalled_at FROM support_messages WHERE idempotency_key=$1 AND conversation_id=$2 AND sender_type=$3 AND sender_id=$4")).
		WithArgs("retry-key", int64(41), "user", int64(7)).WillReturnRows(supportMessageRows(101, 41, 7, "user", "hello", now))
	m, err := h.insertMessage(supportChatTestContext(), 41, 7, "user", supportMessageInput{Content: "hello", Kind: "text", IdempotencyKey: "retry-key"})
	require.NoError(t, err)
	require.Equal(t, int64(101), m.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSupportChatReadClearsUserUnread(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	h := NewSupportChatHandler(db, nil)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE support_conversations SET unread_by_user=0,updated_at=NOW() WHERE user_id=$1")).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	c := supportChatTestContext()
	h.Read(c)
	require.Equal(t, 200, c.Writer.Status())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSupportChatAdminReadClearsAdminUnread(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	h := NewSupportChatHandler(db, nil)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE support_conversations SET unread_by_admin=0,manually_unread_by_admin=false,updated_at=NOW() WHERE id=$1")).
		WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 1))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: "41"}}
	h.AdminRead(c)
	require.Equal(t, 200, c.Writer.Status())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSupportChatAdminConversationsHidesEmptyConversations(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	h := NewSupportChatHandler(db, nil)
	rows := sqlmock.NewRows([]string{"id", "user_id", "email", "username", "unread_by_user", "unread_by_admin", "manually_unread_by_admin", "last_message_at", "updated_at"})
	mock.ExpectQuery(`FROM support_conversations c JOIN users u ON u.id=c.user_id WHERE EXISTS \(SELECT 1 FROM support_messages m WHERE m.conversation_id=c.id\)`).
		WithArgs("", false).
		WillReturnRows(rows)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/admin/chat/conversations", nil)
	h.AdminConversations(c)

	require.Equal(t, 200, c.Writer.Status())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestParseSupportMessageRequestBoundsAndSniffsAttachment(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("content", "see screenshot"))
	part, err := writer.CreateFormFile("file", "../screenshot.png")
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/chat/messages", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	in, upload, err := parseSupportMessageRequest(c)
	require.NoError(t, err)
	require.Equal(t, "see screenshot", in.Content)
	require.Equal(t, "screenshot.png", upload.Filename)
	require.Equal(t, "image/png", upload.ContentType)
	require.Len(t, upload.Data, 12)
}

func TestParseSupportMessageRequestAcceptsPlainTextWithCharset(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "notes.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("plain text attachment"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/chat/messages", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	_, upload, err := parseSupportMessageRequest(c)
	require.NoError(t, err)
	require.Equal(t, "text/plain", upload.ContentType)
}

func TestParseSupportMessageRequestAcceptsWordDocuments(t *testing.T) {
	t.Run("docx", func(t *testing.T) {
		var document bytes.Buffer
		archive := zip.NewWriter(&document)
		contentTypes, err := archive.Create("[Content_Types].xml")
		require.NoError(t, err)
		_, err = contentTypes.Write([]byte(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`))
		require.NoError(t, err)
		wordDocument, err := archive.Create("word/document.xml")
		require.NoError(t, err)
		_, err = wordDocument.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`))
		require.NoError(t, err)
		require.NoError(t, archive.Close())

		_, upload, err := parseSupportAttachmentForTest(t, "guide.docx", document.Bytes())
		require.NoError(t, err)
		require.Equal(t, supportChatDOCXType, upload.ContentType)
	})

	t.Run("doc", func(t *testing.T) {
		data := append(append([]byte(nil), supportChatOLEHeader...), bytes.Repeat([]byte{0}, 512)...)
		_, upload, err := parseSupportAttachmentForTest(t, "guide.doc", data)
		require.NoError(t, err)
		require.Equal(t, supportChatDOCType, upload.ContentType)
	})
}

func TestParseSupportMessageRequestRejectsRenamedZipAsDOCX(t *testing.T) {
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	entry, err := archive.Create("unrelated.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("not a Word document"))
	require.NoError(t, err)
	require.NoError(t, archive.Close())

	_, _, err = parseSupportAttachmentForTest(t, "fake.docx", document.Bytes())
	require.EqualError(t, err, "unsupported file type")
}

func parseSupportAttachmentForTest(t *testing.T, filename string, data []byte) (supportMessageInput, *supportAttachmentUpload, error) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/chat/messages", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return parseSupportMessageRequest(c)
}

func TestParseSupportMessageRequestCapsJSONBody(t *testing.T) {
	body := `{"content":"` + strings.Repeat("x", supportChatMaxJSONRequest) + `"}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/chat/messages", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	_, _, err := parseSupportMessageRequest(c)
	require.Error(t, err)
}

func TestSupportChatMessageRateLimitIsTenPerMinute(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	h := NewSupportChatHandler(nil, redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	c := supportChatTestContext()
	for i := 0; i < supportChatMessagesPerMinute; i++ {
		allowed, _ := h.checkMessageRateLimit(c, 7, "user")
		require.True(t, allowed)
	}
	allowed, retryAfter := h.checkMessageRateLimit(c, 7, "user")
	require.False(t, allowed)
	require.Greater(t, retryAfter, time.Duration(0))
}

func TestSupportChatAttachmentMessagePersistsAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	h := NewSupportChatHandler(db, nil)
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO support_messages .*VALUES \(\$1,\$2,\$3,\$4,'file',\$5\).*RETURNING`).
		WithArgs(int64(41), "user", int64(7), "", "file-key").
		WillReturnRows(supportMessageRows(201, 41, 7, "user", "", now))
	mock.ExpectQuery(`INSERT INTO support_attachments .*RETURNING id`).
		WithArgs(int64(201), int64(41), "user", int64(7), "shot.png", "image/png", 4, []byte("png!")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(301)))
	mock.ExpectExec(`UPDATE support_conversations SET unread_by_admin=unread_by_admin\+1,last_message_at=\$2,updated_at=\$2 WHERE id=\$1`).
		WithArgs(int64(41), now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	m, err := h.insertMessage(supportChatTestContext(), 41, 7, "user", supportMessageInput{IdempotencyKey: "file-key"}, &supportAttachmentUpload{Filename: "shot.png", ContentType: "image/png", Data: []byte("png!")})
	require.NoError(t, err)
	require.Equal(t, int64(301), m.Attachment.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}
