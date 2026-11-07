-- One-way customer feedback is stored independently from support chat.
CREATE TABLE IF NOT EXISTS feedback_submissions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category VARCHAR(16) NOT NULL CHECK (category IN ('problem', 'suggestion', 'other')),
    subject VARCHAR(100) NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 100),
    content TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 5000),
    idempotency_key VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_feedback_submissions_recent
    ON feedback_submissions (created_at DESC, id DESC);
