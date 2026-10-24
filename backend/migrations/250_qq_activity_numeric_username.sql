-- Treat a plain QQ profile username and its numeric QQ email as one identity.
-- Keep migration 249 unchanged; its triggers use this replaced function.
LOCK TABLE users,qq_activity_bindings IN SHARE ROW EXCLUSIVE MODE;

CREATE OR REPLACE FUNCTION qq_activity_account_qqs(account_email TEXT, account_username TEXT)
RETURNS TABLE(qq TEXT) LANGUAGE sql IMMUTABLE AS $$
    SELECT DISTINCT split_part(lower(btrim(value)), '@', 1)
    FROM (VALUES (account_email,FALSE), (account_username,TRUE)) AS values_to_check(value,is_username)
    WHERE lower(btrim(value)) ~ '^[1-9][0-9]{4,14}@qq[.]com$'
        OR (is_username AND btrim(value) ~ '^[1-9][0-9]{4,14}$')
$$;

DO $$
DECLARE identity RECORD;
BEGIN
    IF EXISTS (
        SELECT qq FROM (
            SELECT k.qq,u.id AS user_id FROM users u
            CROSS JOIN LATERAL qq_activity_account_qqs(u.email,u.username) k
            WHERE u.deleted_at IS NULL
            UNION SELECT qq,user_id FROM qq_activity_bindings
            UNION SELECT qq,user_id FROM qq_activity_identity_owners
        ) identities GROUP BY qq HAVING COUNT(DISTINCT user_id)>1
    ) THEN
        RAISE EXCEPTION 'Existing numeric QQ identity conflict requires reconciliation';
    END IF;
    FOR identity IN
        SELECT k.qq,u.id AS user_id FROM users u
        CROSS JOIN LATERAL qq_activity_account_qqs(u.email,u.username) k
        WHERE u.deleted_at IS NULL
        ORDER BY k.qq,u.id
    LOOP
        PERFORM qq_activity_reserve_identity(identity.qq,identity.user_id);
    END LOOP;
END
$$;
