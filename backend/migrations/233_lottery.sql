-- Configurable lottery rounds. Runtime configuration is copied into each round
-- so an administrator can edit the next round without changing an active one.
INSERT INTO settings (key, value)
VALUES ('lottery_enabled', 'false')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS lottery_config (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    participant_threshold INTEGER NOT NULL DEFAULT 50 CHECK (participant_threshold BETWEEN 2 AND 100000),
    prize_count INTEGER NOT NULL DEFAULT 1 CHECK (prize_count BETWEEN 1 AND 10000),
    prize_amount DECIMAL(20,8) NOT NULL DEFAULT 5 CHECK (prize_amount > 0),
    draw_mode VARCHAR(16) NOT NULL DEFAULT 'auto' CHECK (draw_mode IN ('auto', 'manual')),
    next_round_mode VARCHAR(16) NOT NULL DEFAULT 'manual' CHECK (next_round_mode IN ('auto', 'manual')),
    actor_percentage DECIMAL(5,2) NOT NULL DEFAULT 0 CHECK (actor_percentage BETWEEN 0 AND 95),
    actor_join_min_seconds INTEGER NOT NULL DEFAULT 60 CHECK (actor_join_min_seconds BETWEEN 5 AND 86400),
    actor_join_max_seconds INTEGER NOT NULL DEFAULT 600 CHECK (actor_join_max_seconds BETWEEN 5 AND 86400),
    require_recharge BOOLEAN NOT NULL DEFAULT TRUE,
    min_recharge_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (min_recharge_amount >= 0),
    min_account_age_days INTEGER NOT NULL DEFAULT 0 CHECK (min_account_age_days BETWEEN 0 AND 36500),
    recent_recharge_days INTEGER NOT NULL DEFAULT 0 CHECK (recent_recharge_days BETWEEN 0 AND 36500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (actor_join_max_seconds >= actor_join_min_seconds)
);

INSERT INTO lottery_config (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS lottery_rounds (
    id BIGSERIAL PRIMARY KEY,
    round_no BIGINT NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'drawn', 'cancelled')),
    participant_threshold INTEGER NOT NULL CHECK (participant_threshold >= 2),
    prize_count INTEGER NOT NULL CHECK (prize_count >= 1),
    prize_amount DECIMAL(20,8) NOT NULL CHECK (prize_amount > 0),
    draw_mode VARCHAR(16) NOT NULL CHECK (draw_mode IN ('auto', 'manual')),
    next_round_mode VARCHAR(16) NOT NULL CHECK (next_round_mode IN ('auto', 'manual')),
    actor_percentage DECIMAL(5,2) NOT NULL CHECK (actor_percentage BETWEEN 0 AND 95),
    actor_target_count INTEGER NOT NULL DEFAULT 0 CHECK (actor_target_count >= 0),
    actor_join_min_seconds INTEGER NOT NULL CHECK (actor_join_min_seconds >= 5),
    actor_join_max_seconds INTEGER NOT NULL CHECK (actor_join_max_seconds >= actor_join_min_seconds),
    require_recharge BOOLEAN NOT NULL,
    min_recharge_amount DECIMAL(20,8) NOT NULL CHECK (min_recharge_amount >= 0),
    min_account_age_days INTEGER NOT NULL CHECK (min_account_age_days >= 0),
    recent_recharge_days INTEGER NOT NULL CHECK (recent_recharge_days >= 0),
    participant_count INTEGER NOT NULL DEFAULT 0 CHECK (participant_count >= 0),
    actor_count INTEGER NOT NULL DEFAULT 0 CHECK (actor_count >= 0),
    winner_count INTEGER NOT NULL DEFAULT 0 CHECK (winner_count >= 0),
    next_actor_at TIMESTAMPTZ NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    drawn_at TIMESTAMPTZ NULL,
    created_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_lottery_rounds_one_open
    ON lottery_rounds ((status)) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_lottery_rounds_recent
    ON lottery_rounds (round_no DESC);

CREATE TABLE IF NOT EXISTS lottery_participants (
    id BIGSERIAL PRIMARY KEY,
    round_id BIGINT NOT NULL REFERENCES lottery_rounds(id) ON DELETE CASCADE,
    user_id BIGINT NULL REFERENCES users(id) ON DELETE RESTRICT,
    is_actor BOOLEAN NOT NULL DEFAULT FALSE,
    client_ip VARCHAR(64) NULL,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((is_actor AND user_id IS NULL) OR (NOT is_actor AND user_id IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_lottery_participants_round_user
    ON lottery_participants (round_id, user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_lottery_participants_round_joined
    ON lottery_participants (round_id, joined_at, id);
CREATE INDEX IF NOT EXISTS idx_lottery_participants_round_real
    ON lottery_participants (round_id, is_actor, id);

CREATE TABLE IF NOT EXISTS lottery_winners (
    id BIGSERIAL PRIMARY KEY,
    round_id BIGINT NOT NULL REFERENCES lottery_rounds(id) ON DELETE RESTRICT,
    participant_id BIGINT NOT NULL REFERENCES lottery_participants(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    email_snapshot VARCHAR(255) NOT NULL,
    prize_amount DECIMAL(20,8) NOT NULL CHECK (prize_amount > 0),
    balance_before DECIMAL(20,8) NOT NULL,
    balance_after DECIMAL(20,8) NOT NULL,
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (round_id, participant_id),
    UNIQUE (round_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_lottery_winners_recent
    ON lottery_winners (awarded_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_lottery_winners_user_recent
    ON lottery_winners (user_id, awarded_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS lottery_balance_ledger (
    id BIGSERIAL PRIMARY KEY,
    winner_id BIGINT NOT NULL UNIQUE REFERENCES lottery_winners(id) ON DELETE RESTRICT,
    round_id BIGINT NOT NULL REFERENCES lottery_rounds(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount DECIMAL(20,8) NOT NULL CHECK (amount > 0),
    balance_before DECIMAL(20,8) NOT NULL,
    balance_after DECIMAL(20,8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_lottery_balance_ledger_user
    ON lottery_balance_ledger (user_id, created_at DESC, id DESC);
