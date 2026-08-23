CREATE TABLE IF NOT EXISTS support_conversations (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    unread_by_user INTEGER NOT NULL DEFAULT 0,
    unread_by_admin INTEGER NOT NULL DEFAULT 0,
    manually_unread_by_admin BOOLEAN NOT NULL DEFAULT FALSE,
    last_message_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS support_messages (
    id BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES support_conversations(id) ON DELETE CASCADE,
    sender_type VARCHAR(16) NOT NULL CHECK (sender_type IN ('user', 'admin')),
    sender_id BIGINT NOT NULL,
    content TEXT NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'text',
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    recalled_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_support_messages_conversation_created
    ON support_messages (conversation_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_support_conversations_unread_admin
    ON support_conversations (unread_by_admin, last_message_at DESC NULLS LAST);
