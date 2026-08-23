package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSupportChatAttachmentsMigration(t *testing.T) {
	content, err := FS.ReadFile("231_support_chat_attachments.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS support_attachments")
	require.Contains(t, sql, "message_id BIGINT NOT NULL UNIQUE REFERENCES support_messages(id) ON DELETE CASCADE")
	require.Contains(t, sql, "conversation_id BIGINT NOT NULL REFERENCES support_conversations(id) ON DELETE CASCADE")
	require.Contains(t, sql, "CHECK (uploader_type IN ('user', 'admin'))")
	require.Contains(t, sql, "size_bytes INTEGER NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 4194304)")
	require.Contains(t, sql, "data BYTEA NOT NULL")
	require.Contains(t, sql, "idx_support_attachments_conversation")
}
