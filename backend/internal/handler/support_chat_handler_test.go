package handler

import (
	"database/sql"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func supportChatTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
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

	h := NewSupportChatHandler(db)
	_, err = h.insertMessage(supportChatTestContext(), 41, 7, "user", supportMessageInput{Content: " \n\t"})
	require.EqualError(t, err, "message content must be between 1 and 10000 characters")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSupportChatInsertMessageUpdatesUnreadForEachSender(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	h := NewSupportChatHandler(db)
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
	h := NewSupportChatHandler(db)
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
	h := NewSupportChatHandler(db)
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
	h := NewSupportChatHandler(db)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE support_conversations SET unread_by_admin=0,manually_unread_by_admin=false,updated_at=NOW() WHERE id=$1")).
		WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 1))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: "41"}}
	h.AdminRead(c)
	require.Equal(t, 200, c.Writer.Status())
	require.NoError(t, mock.ExpectationsWereMet())
}
