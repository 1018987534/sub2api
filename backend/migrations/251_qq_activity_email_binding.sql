-- Existing bindings, reward history and registered-email ownership stay intact.
-- A username is now a display name, not evidence of QQ mailbox ownership.
LOCK TABLE users,qq_activity_bindings IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE qq_activity_binding_history (
    id BIGSERIAL PRIMARY KEY,
    qq TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    source TEXT NOT NULL,
    bound_at TIMESTAMPTZ NOT NULL,
    unbound_at TIMESTAMPTZ
);
CREATE INDEX qq_activity_binding_history_qq_idx ON qq_activity_binding_history(qq,user_id);
CREATE INDEX qq_activity_binding_history_user_idx ON qq_activity_binding_history(user_id);
INSERT INTO qq_activity_binding_history(qq,user_id,source,bound_at)
SELECT qq,user_id,source,bound_at FROM qq_activity_bindings;

CREATE FUNCTION qq_activity_record_binding_history() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        INSERT INTO qq_activity_binding_history(qq,user_id,source,bound_at)
        VALUES(NEW.qq,NEW.user_id,NEW.source,NEW.bound_at);
        RETURN NEW;
    END IF;
    UPDATE qq_activity_binding_history SET unbound_at=clock_timestamp()
    WHERE qq=OLD.qq AND user_id=OLD.user_id AND unbound_at IS NULL;
    RETURN OLD;
END
$$;
CREATE TRIGGER qq_activity_binding_history_insert AFTER INSERT ON qq_activity_bindings
FOR EACH ROW EXECUTE FUNCTION qq_activity_record_binding_history();
CREATE TRIGGER qq_activity_binding_history_delete BEFORE DELETE ON qq_activity_bindings
FOR EACH ROW EXECUTE FUNCTION qq_activity_record_binding_history();

CREATE OR REPLACE FUNCTION qq_activity_account_qqs(account_email TEXT, account_username TEXT)
RETURNS TABLE(qq TEXT) LANGUAGE sql IMMUTABLE AS $$
    SELECT split_part(lower(btrim(account_email)), '@', 1)
    WHERE lower(btrim(account_email)) ~ '^[1-9][0-9]{4,14}@qq[.]com$'
$$;

DELETE FROM qq_activity_identity_owners o
WHERE NOT EXISTS (SELECT 1 FROM qq_activity_bindings b WHERE b.qq=o.qq AND b.user_id=o.user_id)
  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id=o.user_id AND u.deleted_at IS NULL
                  AND lower(btrim(u.email))=o.qq||'@qq.com');

CREATE OR REPLACE FUNCTION qq_activity_sync_user_identities()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE identity_qq TEXT;
BEGIN
    IF NEW.deleted_at IS NULL THEN
        FOR identity_qq IN SELECT qq FROM qq_activity_account_qqs(NEW.email,NEW.username) ORDER BY qq LOOP
            PERFORM qq_activity_reserve_identity(identity_qq,NEW.id);
        END LOOP;
    END IF;
    -- A self-service unbind never releases historical QQ ownership or rewards.
    DELETE FROM qq_activity_identity_owners o WHERE o.user_id=NEW.id
        AND NOT EXISTS (SELECT 1 FROM qq_activity_bindings b WHERE b.qq=o.qq AND b.user_id=o.user_id)
        AND NOT EXISTS (SELECT 1 FROM qq_activity_binding_history h WHERE h.qq=o.qq AND h.user_id=o.user_id)
        AND (NEW.deleted_at IS NOT NULL OR NOT EXISTS (
            SELECT 1 FROM qq_activity_account_qqs(NEW.email,NEW.username) k WHERE k.qq=o.qq
        ));
    RETURN NEW;
END
$$;

CREATE TABLE qq_activity_mail_codes (
    id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    qq TEXT NOT NULL CHECK (qq ~ '^[1-9][0-9]{4,14}$'),
    code_hash TEXT NOT NULL,
    ip_hash TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    sent_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ
);
CREATE INDEX qq_activity_mail_codes_user_idx ON qq_activity_mail_codes(user_id,created_at DESC);
CREATE INDEX qq_activity_mail_codes_qq_idx ON qq_activity_mail_codes(qq,created_at DESC);
CREATE INDEX qq_activity_mail_codes_ip_idx ON qq_activity_mail_codes(ip_hash,created_at DESC);
