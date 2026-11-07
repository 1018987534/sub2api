-- QQ membership identity and a website account are separate namespaces.
-- This migration never changes a website account's email or login credentials.
LOCK TABLE users,qq_activity_bindings,qq_activity_binding_history IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE IF NOT EXISTS qq_activity_ever_bound (
    identity_kind TEXT NOT NULL CHECK(identity_kind IN ('qq','user','email')),
    identity_key TEXT NOT NULL,
    first_bound_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(identity_kind,identity_key)
);
CREATE TABLE IF NOT EXISTS qq_activity_first_bindings (
    qq TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE,
    email_digest TEXT NOT NULL UNIQUE,
    bound_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ
);
ALTER TABLE qq_activity_binding_history ADD COLUMN IF NOT EXISTS email_digest TEXT NOT NULL DEFAULT '';
UPDATE qq_activity_binding_history h SET email_digest=encode(sha256(convert_to(lower(btrim(u.email)),'UTF8')),'hex')
FROM users u WHERE u.id=h.user_id AND h.email_digest='';
UPDATE qq_activity_binding_history h SET email_digest=encode(sha256(convert_to(lower(btrim(u.email)),'UTF8')),'hex')
FROM qq_activity_baseline_users u WHERE u.user_id=h.user_id AND h.email_digest='';

INSERT INTO qq_activity_ever_bound(identity_kind,identity_key,first_bound_at)
SELECT 'qq',qq,MIN(bound_at) FROM qq_activity_binding_history GROUP BY qq
UNION ALL SELECT 'user',user_id::text,MIN(bound_at) FROM qq_activity_binding_history GROUP BY user_id
UNION ALL SELECT 'email',email_digest,MIN(bound_at) FROM qq_activity_binding_history WHERE email_digest<>'' GROUP BY email_digest
ON CONFLICT DO NOTHING;
INSERT INTO qq_activity_ever_bound(identity_kind,identity_key,first_bound_at)
SELECT 'qq',subject_qq,MIN(qualified_at) FROM qq_activity_rewards GROUP BY subject_qq
UNION ALL SELECT 'user',subject_user_id::text,MIN(qualified_at) FROM qq_activity_rewards GROUP BY subject_user_id
UNION ALL SELECT 'qq',recipient_qq,MIN(qualified_at) FROM qq_activity_rewards GROUP BY recipient_qq
UNION ALL SELECT 'user',recipient_user_id::text,MIN(qualified_at) FROM qq_activity_rewards WHERE recipient_user_id IS NOT NULL GROUP BY recipient_user_id
ON CONFLICT DO NOTHING;
INSERT INTO qq_activity_ever_bound(identity_kind,identity_key)
SELECT DISTINCT 'email',encode(sha256(convert_to(lower(btrim(u.email)),'UTF8')),'hex') FROM users u
WHERE EXISTS(SELECT 1 FROM qq_activity_ever_bound e WHERE e.identity_kind='user' AND e.identity_key=u.id::text)
ON CONFLICT DO NOTHING;
INSERT INTO qq_activity_ever_bound(identity_kind,identity_key)
SELECT DISTINCT 'email',encode(sha256(convert_to(lower(btrim(b.email)),'UTF8')),'hex') FROM qq_activity_baseline_users b
WHERE EXISTS(SELECT 1 FROM qq_activity_ever_bound e WHERE e.identity_kind='user' AND e.identity_key=b.user_id::text)
ON CONFLICT DO NOTHING;

-- Only an uninterrupted original first binding can continue its original
-- eligibility checks. Closed/replaced histories never reopen an opportunity.
INSERT INTO qq_activity_first_bindings(qq,user_id,email_digest,bound_at)
SELECT b.qq,b.user_id,h.email_digest,h.bound_at FROM qq_activity_bindings b
JOIN qq_activity_binding_history h ON h.qq=b.qq AND h.user_id=b.user_id AND h.unbound_at IS NULL
WHERE NOT EXISTS(SELECT 1 FROM qq_activity_binding_history x WHERE x.id<>h.id AND (x.qq=h.qq OR x.user_id=h.user_id OR (h.email_digest<>'' AND x.email_digest=h.email_digest)))
AND NOT EXISTS(SELECT 1 FROM users x WHERE x.id<>h.user_id AND encode(sha256(convert_to(lower(btrim(x.email)),'UTF8')),'hex')=h.email_digest
    AND EXISTS(SELECT 1 FROM qq_activity_ever_bound e WHERE e.identity_kind='user' AND e.identity_key=x.id::text))
AND NOT EXISTS(SELECT 1 FROM qq_activity_baseline_users x WHERE x.user_id<>h.user_id AND encode(sha256(convert_to(lower(btrim(x.email)),'UTF8')),'hex')=h.email_digest
    AND EXISTS(SELECT 1 FROM qq_activity_ever_bound e WHERE e.identity_kind='user' AND e.identity_key=x.user_id::text))
AND NOT EXISTS(SELECT 1 FROM qq_activity_rewards r WHERE
    ((r.subject_qq=h.qq OR r.subject_user_id=h.user_id) AND (r.subject_qq<>h.qq OR r.subject_user_id<>h.user_id OR r.qualified_at<h.bound_at))
    OR (r.recipient_user_id IS NOT NULL AND (r.recipient_qq=h.qq OR r.recipient_user_id=h.user_id) AND (r.recipient_qq<>h.qq OR r.recipient_user_id<>h.user_id OR r.qualified_at<h.bound_at)))
ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION qq_activity_record_binding_history() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE digest TEXT; seen BOOLEAN;
BEGIN
    IF TG_OP='INSERT' THEN
        SELECT encode(sha256(convert_to(lower(btrim(email)),'UTF8')),'hex') INTO digest FROM users WHERE id=NEW.user_id;
        IF digest IS NULL THEN RAISE EXCEPTION 'Binding account missing'; END IF;
        -- Check BEFORE appending the permanent flags. A first binding gets
        -- its normal assessment even though its history exists immediately.
        SELECT EXISTS(SELECT 1 FROM qq_activity_ever_bound WHERE
            (identity_kind='qq' AND identity_key=NEW.qq) OR
            (identity_kind='user' AND identity_key=NEW.user_id::text) OR
            (identity_kind='email' AND identity_key=digest)) INTO seen;
        IF NOT seen THEN
            INSERT INTO qq_activity_first_bindings(qq,user_id,email_digest,bound_at)
            VALUES(NEW.qq,NEW.user_id,digest,NEW.bound_at) ON CONFLICT DO NOTHING;
        END IF;
        INSERT INTO qq_activity_ever_bound(identity_kind,identity_key,first_bound_at)
        VALUES('qq',NEW.qq,NEW.bound_at),('user',NEW.user_id::text,NEW.bound_at),('email',digest,NEW.bound_at)
        ON CONFLICT DO NOTHING;
        INSERT INTO qq_activity_binding_history(qq,user_id,source,bound_at,email_digest)
        VALUES(NEW.qq,NEW.user_id,NEW.source,NEW.bound_at,digest);
        RETURN NEW;
    END IF;
    UPDATE qq_activity_binding_history SET unbound_at=clock_timestamp()
    WHERE qq=OLD.qq AND user_id=OLD.user_id AND unbound_at IS NULL;
    UPDATE qq_activity_first_bindings SET closed_at=clock_timestamp()
    WHERE qq=OLD.qq AND user_id=OLD.user_id AND closed_at IS NULL;
    RETURN OLD;
END $$;

-- Private binding proves the event sender's QQ and the destination account's
-- registered mailbox independently. The older website mailbox flow retains
-- its existing numeric-mailbox ownership semantics.
CREATE OR REPLACE FUNCTION qq_activity_guard_binding_identity() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.qq<>OLD.qq OR NEW.user_id<>OLD.user_id) THEN
        RAISE EXCEPTION 'Replace bindings by transactional delete/insert to retain history';
    END IF;
    IF NEW.source<>'verified_private_email' THEN
        PERFORM qq_activity_reserve_identity(NEW.qq,NEW.user_id);
    END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION qq_activity_remember_bound_email() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM qq_activity_ever_bound WHERE identity_kind='user' AND identity_key=OLD.id::text) THEN
        INSERT INTO qq_activity_ever_bound(identity_kind,identity_key)
        VALUES('email',encode(sha256(convert_to(lower(btrim(OLD.email)),'UTF8')),'hex')) ON CONFLICT DO NOTHING;
        IF TG_OP='UPDATE' THEN
            INSERT INTO qq_activity_ever_bound(identity_kind,identity_key)
            VALUES('email',encode(sha256(convert_to(lower(btrim(NEW.email)),'UTF8')),'hex')) ON CONFLICT DO NOTHING;
        END IF;
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS qq_activity_bound_email_history ON users;
CREATE TRIGGER qq_activity_bound_email_history BEFORE UPDATE OF email,deleted_at OR DELETE ON users
FOR EACH ROW EXECUTE FUNCTION qq_activity_remember_bound_email();

CREATE TABLE IF NOT EXISTS qq_activity_private_sessions (
    qq TEXT PRIMARY KEY,
    step TEXT NOT NULL,
    target_user_id BIGINT,
    target_email TEXT NOT NULL DEFAULT '',
    original_user_id BIGINT,
    original_bound_at TIMESTAMPTZ,
    challenge TEXT NOT NULL DEFAULT '',
    code_hash TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    sent_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS qq_activity_private_mail_sends (
    challenge TEXT PRIMARY KEY,
    qq TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS qq_activity_private_mail_qq_idx ON qq_activity_private_mail_sends(qq,created_at DESC);
CREATE INDEX IF NOT EXISTS qq_activity_private_mail_user_idx ON qq_activity_private_mail_sends(user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS qq_activity_private_events (
    event_id TEXT PRIMARY KEY,
    qq TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE qq_activity_invitations ADD COLUMN IF NOT EXISTS inviter_user_id BIGINT;
UPDATE qq_activity_invitations i SET inviter_user_id=r.recipient_user_id
FROM qq_activity_rewards r WHERE r.kind='invitation' AND r.subject_qq=i.invitee_qq AND i.inviter_user_id IS NULL;
UPDATE qq_activity_invitations i SET inviter_user_id=(SELECT h.user_id FROM qq_activity_binding_history h WHERE h.qq=i.inviter_qq ORDER BY CASE WHEN h.bound_at<=i.joined_at THEN 0 ELSE 1 END,CASE WHEN h.bound_at<=i.joined_at THEN h.bound_at END DESC,h.bound_at,h.id LIMIT 1)
WHERE i.inviter_user_id IS NULL;
ALTER TABLE qq_activity_outbox ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'group' CHECK(scope IN ('group','private'));

-- Replace shipped defaults only; preserve administrator-authored copy.
UPDATE qq_activity_config SET display_binding_intro='注册 QQ 邮箱匹配到群成员时仍会自动绑定。也可私信管理员 Nono，发送【绑定】，验证任意官网注册邮箱后绑定。'
WHERE display_binding_intro='注册 QQ 邮箱能匹配到群成员时会自动绑定，已有绑定继续有效。使用其他邮箱注册的，请在此验证本人 QQ 邮箱。';
UPDATE qq_activity_config SET display_binding_note='每个 QQ 只能关联一个官网账号。换绑请私信 Nono；注册邮箱保持不变。曾绑定过的 QQ 或官网账号不再获得新人资格，换绑不会重置奖励。'
WHERE display_binding_note='每个 QQ 只能关联一个官网账号。注册邮箱保持不变，修改个人资料中的用户名不再用于绑定。';
