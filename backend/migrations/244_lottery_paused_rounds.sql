ALTER TABLE lottery_rounds DROP CONSTRAINT IF EXISTS lottery_rounds_status_check;
ALTER TABLE lottery_rounds ADD CONSTRAINT lottery_rounds_status_check
    CHECK (status IN ('open', 'paused', 'drawn', 'cancelled'));

-- A paused round still owns the active slot until resumed, drawn, or voided.
CREATE UNIQUE INDEX IF NOT EXISTS idx_lottery_rounds_one_active
    ON lottery_rounds ((TRUE)) WHERE status IN ('open', 'paused');
DROP INDEX IF EXISTS idx_lottery_rounds_one_open;
