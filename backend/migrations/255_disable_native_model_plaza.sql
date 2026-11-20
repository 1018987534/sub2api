-- Switch existing installations to the separate display-only custom catalog.
-- Keep the native feature and its settings intact so it can still be re-enabled.
UPDATE settings
SET value = 'false', updated_at = NOW()
WHERE key = 'model_plaza_enabled' AND value = 'true';
