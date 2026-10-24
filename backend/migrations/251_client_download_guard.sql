-- Persistent reservations: application restarts never reset download budgets.
CREATE TABLE client_download_budgets (
    bucket TEXT PRIMARY KEY,
    used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    used_count BIGINT NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX client_download_budgets_expiry_idx ON client_download_budgets(expires_at);
CREATE TABLE client_download_tickets (
    token_sha256 TEXT PRIMARY KEY CHECK (length(token_sha256) = 64),
    user_id BIGINT NOT NULL,
    file_path TEXT NOT NULL,
    filename TEXT NOT NULL,
    bytes BIGINT NOT NULL CHECK (bytes > 0),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX client_download_tickets_user_idx ON client_download_tickets(user_id);
CREATE INDEX client_download_tickets_expiry_idx ON client_download_tickets(expires_at);
