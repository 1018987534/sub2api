-- One numeric QQ identity across registration email, profile username and permanent bindings.
-- The primary key arbitrates concurrent registrations/profile edits in the database.
LOCK TABLE users,qq_activity_bindings IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE qq_activity_identity_owners (
    qq TEXT PRIMARY KEY CHECK (qq ~ '^[1-9][0-9]{4,14}$'),
    user_id BIGINT NOT NULL
);
CREATE INDEX qq_activity_identity_owners_user_idx ON qq_activity_identity_owners(user_id);

CREATE FUNCTION qq_activity_account_qqs(account_email TEXT, account_username TEXT)
RETURNS TABLE(qq TEXT) LANGUAGE sql IMMUTABLE AS $$
    SELECT DISTINCT split_part(lower(btrim(value)), '@', 1)
    FROM (VALUES (account_email), (account_username)) AS values_to_check(value)
    WHERE lower(btrim(value)) ~ '^[1-9][0-9]{4,14}@qq[.]com$'
$$;

CREATE FUNCTION qq_activity_reserve_identity(identity_qq TEXT, identity_user BIGINT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO qq_activity_identity_owners(qq,user_id) VALUES(identity_qq,identity_user)
    ON CONFLICT(qq) DO UPDATE SET user_id=EXCLUDED.user_id
    WHERE qq_activity_identity_owners.user_id=EXCLUDED.user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23505',
            CONSTRAINT='qq_activity_identity_owner_unique',
            MESSAGE='QQ identity already in use';
    END IF;
END
$$;

-- Refuse to assign an existing ambiguous identity to an arbitrary account.
DO $$
DECLARE identity RECORD;
BEGIN
    IF EXISTS (
        SELECT qq FROM (
            SELECT k.qq,u.id AS user_id FROM users u
            CROSS JOIN LATERAL qq_activity_account_qqs(u.email,u.username) k
            WHERE u.deleted_at IS NULL
            UNION SELECT qq,user_id FROM qq_activity_bindings
        ) identities GROUP BY qq HAVING COUNT(DISTINCT user_id)>1
    ) THEN
        RAISE EXCEPTION 'Existing QQ identity conflict requires reconciliation';
    END IF;
    FOR identity IN
        SELECT k.qq,u.id AS user_id FROM users u
        CROSS JOIN LATERAL qq_activity_account_qqs(u.email,u.username) k
        WHERE u.deleted_at IS NULL
        UNION SELECT qq,user_id FROM qq_activity_bindings
        ORDER BY qq
    LOOP
        PERFORM qq_activity_reserve_identity(identity.qq,identity.user_id);
    END LOOP;
END
$$;

CREATE FUNCTION qq_activity_sync_user_identities()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE identity_qq TEXT;
BEGIN
    IF NEW.deleted_at IS NULL THEN
        FOR identity_qq IN SELECT qq FROM qq_activity_account_qqs(NEW.email,NEW.username) ORDER BY qq LOOP
            PERFORM qq_activity_reserve_identity(identity_qq,NEW.id);
        END LOOP;
    END IF;
    -- An established binding retains its identity even after rename/deletion.
    DELETE FROM qq_activity_identity_owners o WHERE o.user_id=NEW.id
        AND NOT EXISTS (SELECT 1 FROM qq_activity_bindings b WHERE b.qq=o.qq AND b.user_id=o.user_id)
        AND (NEW.deleted_at IS NOT NULL OR NOT EXISTS (
            SELECT 1 FROM qq_activity_account_qqs(NEW.email,NEW.username) k WHERE k.qq=o.qq
        ));
    RETURN NEW;
END
$$;
CREATE TRIGGER qq_activity_user_identity_guard
BEFORE INSERT OR UPDATE OF email,username,deleted_at ON users
FOR EACH ROW EXECUTE FUNCTION qq_activity_sync_user_identities();

CREATE FUNCTION qq_activity_guard_binding_identity()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM qq_activity_reserve_identity(NEW.qq,NEW.user_id);
    RETURN NEW;
END
$$;
CREATE TRIGGER qq_activity_binding_identity_guard
BEFORE INSERT OR UPDATE OF qq,user_id ON qq_activity_bindings
FOR EACH ROW EXECUTE FUNCTION qq_activity_guard_binding_identity();
