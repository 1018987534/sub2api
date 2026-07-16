ALTER TABLE user_affiliates
    ADD COLUMN IF NOT EXISTS inviter_bound_at TIMESTAMPTZ NULL;

ALTER TABLE user_affiliates
    ADD COLUMN IF NOT EXISTS inviter_bind_source VARCHAR(16) NULL;

UPDATE user_affiliates
SET inviter_bound_at = COALESCE(inviter_bound_at, created_at),
    inviter_bind_source = COALESCE(NULLIF(inviter_bind_source, ''), 'registration')
WHERE inviter_id IS NOT NULL
  AND (inviter_bound_at IS NULL OR inviter_bind_source IS NULL OR inviter_bind_source = '');

COMMENT ON COLUMN user_affiliates.inviter_bound_at IS '邀请关系实际绑定时间';
COMMENT ON COLUMN user_affiliates.inviter_bind_source IS '邀请关系来源：registration 或 admin';
