ALTER TABLE users
    ADD COLUMN IF NOT EXISTS registration_site_name TEXT NOT NULL DEFAULT '';

UPDATE users
SET registration_site_name = COALESCE(
    (
        SELECT NULLIF(BTRIM(value), '')
        FROM settings
        WHERE key = 'site_name'
        LIMIT 1
    ),
    'Sub2API'
)
WHERE registration_site_name = '';

COMMENT ON COLUMN users.registration_site_name IS '用户注册时请求域名对应的站点标题快照';
