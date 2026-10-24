-- Additive activity tables. Existing referral relationships and recharge rebates are unchanged.
CREATE TABLE IF NOT EXISTS qq_activity_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    status TEXT NOT NULL DEFAULT 'preparing' CHECK (status IN ('preparing','active','paused','ended')),
    initialized_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    newcomer_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    invitation_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    auto_process_join BOOLEAN NOT NULL DEFAULT TRUE,
    group_messages_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO qq_activity_config (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS qq_activity_groups (
    group_id TEXT PRIMARY KEY,
    bot_qq TEXT NOT NULL,
    initialized_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- No cascading deletion: baseline identities and paid rewards survive account deletion.
CREATE TABLE IF NOT EXISTS qq_activity_baseline_users (
    user_id BIGINT PRIMARY KEY,
    registered_at TIMESTAMPTZ NOT NULL,
    email TEXT NOT NULL,
    username TEXT NOT NULL DEFAULT '',
    captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS qq_activity_members (
    group_id TEXT NOT NULL REFERENCES qq_activity_groups(group_id),
    qq TEXT NOT NULL,
    in_group BOOLEAN NOT NULL DEFAULT TRUE,
    is_robot BOOLEAN NOT NULL DEFAULT FALSE,
    legacy BOOLEAN NOT NULL DEFAULT FALSE,
    first_joined_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    qq_level INTEGER CHECK (qq_level > 0),
    level_checked_at TIMESTAMPTZ,
    level_attempts INTEGER NOT NULL DEFAULT 0,
    next_level_retry_at TIMESTAMPTZ,
    candidate_user_id BIGINT,
    match_source TEXT NOT NULL DEFAULT '',
    match_state TEXT NOT NULL DEFAULT 'unmatched',
    PRIMARY KEY (group_id,qq)
);
CREATE INDEX IF NOT EXISTS qq_activity_members_qq_idx ON qq_activity_members(qq);
CREATE TABLE IF NOT EXISTS qq_activity_bindings (
    qq TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE,
    source TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','locked')),
    bound_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS qq_activity_claims (
    token_hash TEXT PRIMARY KEY,
    group_id TEXT NOT NULL,
    qq TEXT NOT NULL,
    candidate_user_id BIGINT NOT NULL,
    code_hash TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS qq_activity_claims_qq_idx ON qq_activity_claims(qq,created_at DESC);
CREATE TABLE IF NOT EXISTS qq_activity_events (
    event_id TEXT PRIMARY KEY,
    group_id TEXT NOT NULL,
    qq TEXT NOT NULL,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS qq_activity_invitations (
    invitee_qq TEXT PRIMARY KEY,
    inviter_qq TEXT NOT NULL DEFAULT '',
    group_id TEXT NOT NULL,
    evidence_type TEXT NOT NULL DEFAULT '',
    evidence_key TEXT NOT NULL DEFAULT '',
    joined_at TIMESTAMPTZ NOT NULL,
    invitee_user_id BIGINT,
    qualified_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'awaiting_evidence',
    reason TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_activity_inviter_idx ON qq_activity_invitations(inviter_qq);
CREATE TABLE IF NOT EXISTS qq_activity_rewards (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('newcomer','invitation')),
    recipient_qq TEXT NOT NULL,
    recipient_user_id BIGINT,
    subject_qq TEXT NOT NULL,
    subject_user_id BIGINT NOT NULL,
    amount NUMERIC(20,8) NOT NULL DEFAULT 2 CHECK (amount = 2),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid')),
    qualified_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    paid_at TIMESTAMPTZ,
    balance_before NUMERIC(20,8),
    balance_after NUMERIC(20,8),
    UNIQUE (kind,subject_qq),
    UNIQUE (kind,subject_user_id)
);
CREATE INDEX IF NOT EXISTS qq_activity_rewards_recipient_idx ON qq_activity_rewards(recipient_qq,status);
CREATE TABLE IF NOT EXISTS qq_activity_outbox (
    id BIGSERIAL PRIMARY KEY,
    dedup_key TEXT NOT NULL UNIQUE,
    group_id TEXT NOT NULL,
    qq TEXT NOT NULL,
    text TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_activity_outbox_pending_idx ON qq_activity_outbox(next_attempt_at) WHERE delivered_at IS NULL;
CREATE TABLE IF NOT EXISTS qq_activity_audit (
    id BIGSERIAL PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
