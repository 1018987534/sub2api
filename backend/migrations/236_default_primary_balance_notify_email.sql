-- Use each user's primary account email as the default low-balance recipient.
-- Existing user-managed recipient lists and the user-level enable/disable flag
-- remain unchanged.
WITH updated_users AS (
  UPDATE users
  SET balance_notify_extra_emails = jsonb_build_array(
        jsonb_build_object(
          'email', BTRIM(email),
          'disabled', false,
          'verified', true
        )
      )::text,
      updated_at = NOW()
  WHERE deleted_at IS NULL
    AND BTRIM(email) <> ''
    AND (
      balance_notify_extra_emails IS NULL
      OR BTRIM(balance_notify_extra_emails) IN ('', '[]', 'null')
    )
  RETURNING id
)
INSERT INTO auth_cache_invalidation_outbox (cache_key)
SELECT DISTINCT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
FROM api_keys AS k
JOIN updated_users AS u ON u.id = k.user_id
WHERE k.deleted_at IS NULL
  AND k.key <> '';
