CREATE TABLE IF NOT EXISTS support_attachments (
    id BIGSERIAL PRIMARY KEY,
    message_id BIGINT NOT NULL UNIQUE REFERENCES support_messages(id) ON DELETE CASCADE,
    conversation_id BIGINT NOT NULL REFERENCES support_conversations(id) ON DELETE CASCADE,
    uploader_type VARCHAR(16) NOT NULL CHECK (uploader_type IN ('user', 'admin')),
    uploader_id BIGINT NOT NULL,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 4194304),
    data BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_support_attachments_conversation
    ON support_attachments (conversation_id, created_at DESC, id DESC);
