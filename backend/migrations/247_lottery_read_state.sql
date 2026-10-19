-- Round and prize reminders are acknowledged independently of participation.
CREATE TABLE IF NOT EXISTS lottery_read_state (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_read_round_id BIGINT NOT NULL DEFAULT 0,
    last_read_winner_id BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Historical awards from older draws must not become fresh notifications on
-- upgrade. Keep the most recent draw eligible for its one-time reminder.
INSERT INTO lottery_read_state (user_id, last_read_winner_id)
SELECT user_id, MAX(id)
FROM lottery_winners
WHERE round_id < (
    SELECT id FROM lottery_rounds WHERE status = 'drawn' ORDER BY round_no DESC LIMIT 1
)
GROUP BY user_id
ON CONFLICT (user_id) DO NOTHING;
