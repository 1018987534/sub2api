package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSupportChatMigration(t *testing.T) {
	content, err := FS.ReadFile("230_support_chat.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS support_conversations")
	require.Contains(t, sql, "user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE")
	require.Contains(t, sql, "unread_by_user INTEGER NOT NULL DEFAULT 0")
	require.Contains(t, sql, "unread_by_admin INTEGER NOT NULL DEFAULT 0")
	require.Contains(t, sql, "manually_unread_by_admin BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS support_messages")
	require.Contains(t, sql, "CHECK (sender_type IN ('user', 'admin'))")
	require.Contains(t, sql, "idempotency_key VARCHAR(128) NOT NULL UNIQUE")
	require.Contains(t, sql, "idx_support_messages_conversation_created")
	require.Contains(t, sql, "idx_support_conversations_unread_admin")
}
