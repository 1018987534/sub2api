-- Replace scheduled actor participation with an administrator-controlled progress offset.
-- Historical actor rows remain for audit compatibility but are never eligible to win.
ALTER TABLE lottery_rounds
    ADD COLUMN IF NOT EXISTS manual_progress_count INTEGER NOT NULL DEFAULT 0
    CHECK (manual_progress_count >= 0);

UPDATE lottery_rounds
SET manual_progress_count = GREATEST(manual_progress_count, actor_count),
    actor_percentage = 0,
    actor_target_count = 0,
    actor_join_min_seconds = 5,
    actor_join_max_seconds = 5,
    actor_count = 0,
    next_actor_at = NULL,
    updated_at = NOW()
WHERE actor_percentage <> 0
   OR actor_target_count <> 0
   OR actor_count <> 0
   OR next_actor_at IS NOT NULL;

UPDATE lottery_config
SET actor_percentage = 0,
    actor_join_min_seconds = 5,
    actor_join_max_seconds = 5,
    updated_at = NOW()
WHERE actor_percentage <> 0
   OR actor_join_min_seconds <> 5
   OR actor_join_max_seconds <> 5;
