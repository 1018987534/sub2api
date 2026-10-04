ALTER TABLE intelligence_check_runs
    ADD COLUMN IF NOT EXISTS protection_eligible BOOLEAN NOT NULL DEFAULT FALSE;

-- Preserve attributed history from the existing non-degraded groups. Future
-- records snapshot eligibility, so renaming a group does not rewrite a streak.
UPDATE intelligence_check_runs r SET protection_eligible = TRUE
FROM groups g WHERE g.id = r.group_id AND g.platform = 'openai'
    AND g.name LIKE '%不降智%' AND r.account_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_intelligence_runs_global_account
    ON intelligence_check_runs(account_id, id DESC) WHERE protection_eligible;
