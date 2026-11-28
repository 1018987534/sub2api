package intelligence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type Store interface {
	Configs(context.Context) ([]Config, error)
	Save(context.Context, Config) (Config, error)
	AddModel(context.Context, Config) (Config, error)
	DeleteModel(context.Context, ProbeKey, int) error
	Claim(context.Context, ProbeKey, bool) (*Claim, error)
	Finish(context.Context, *Claim, Record) error
	History(context.Context, ProbeKey, time.Time, int64, int) ([]Record, error)
	Record(context.Context, int64, int64, time.Time) (Record, error)
	Timelines(context.Context, []Config, time.Time, int) (map[ProbeKey][]TimelineRecord, error)
	Prune(context.Context) error
}

type SQLStore struct {
	DB                 *sql.DB
	Protect            func(context.Context, Config, int64) (bool, error)
	ProtectionEligible func(context.Context, Config) (bool, error)
	PauseThreshold     func(context.Context) (int, error)
	Recover            func(context.Context, int64) error
	RecoveryCutoff     func(context.Context, int64) (time.Time, error)
}

const eligibleGroupSQL = `g.deleted_at IS NULL AND g.status='active' AND POSITION('不降智' IN g.name)>0`

// ReconcileGroupEligibility persists disabled state in both representations. It
// preserves in-flight leases and increments CAS versions only when state changes.
func (s *SQLStore) ReconcileGroupEligibility(ctx context.Context) error {
	const disableIneligible = ` c
 SET enabled=FALSE,config=jsonb_set(c.config,'{enabled}','false'::jsonb),version=c.version+1,updated_at=NOW()
 WHERE (c.enabled OR c.config->'enabled'='true'::jsonb)
	AND NOT EXISTS (SELECT 1 FROM groups g WHERE g.id=c.group_id AND ` + eligibleGroupSQL + `)`
	_, err := s.DB.ExecContext(ctx, `WITH primary_checks AS (UPDATE intelligence_check_configs`+disableIneligible+`)
 UPDATE intelligence_check_model_configs`+disableIneligible)
	return err
}

func (s *SQLStore) Configs(ctx context.Context) ([]Config, error) {
	if err := s.ReconcileGroupEligibility(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.config,c.version,c.group_id,c.probe_id FROM (
 SELECT config,version,group_id,0::bigint AS probe_id FROM intelligence_check_configs
 UNION ALL SELECT config,version,group_id,id FROM intelligence_check_model_configs
 ) c JOIN groups g ON g.id=c.group_id WHERE `+eligibleGroupSQL+` ORDER BY c.group_id,c.probe_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := []Config{}
	for rows.Next() {
		var raw []byte
		var version int
		var key ProbeKey
		if err = rows.Scan(&raw, &version, &key.GroupID, &key.ProbeID); err != nil {
			return nil, err
		}
		c, err := scanConfig(raw, version, key)
		if err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

func (s *SQLStore) Save(ctx context.Context, c Config) (Config, error) {
	return s.save(ctx, c, false)
}

func (s *SQLStore) AddModel(ctx context.Context, c Config) (Config, error) {
	if c.ProbeID != 0 || c.Version != 0 {
		return c, ErrInvalid
	}
	return s.save(ctx, c, true)
}

func (s *SQLStore) save(ctx context.Context, c Config, addModel bool) (Config, error) {
	c.Model = strings.TrimSpace(c.Model)
	if err := c.Validate(); err != nil {
		return c, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	// Serialize config writers for this group, including checks spanning both tables.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('intelligence_check_configs'),hashtext($1::text))`, c.GroupID); err != nil {
		return c, err
	}
	var duplicate, primaryExists bool
	var extraCount int
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM intelligence_check_configs WHERE group_id=$1),
 (SELECT COUNT(*) FROM intelligence_check_model_configs WHERE group_id=$1),
 EXISTS(SELECT 1 FROM intelligence_check_configs WHERE group_id=$1 AND $3 AND btrim(config->>'model')=$2)
 OR EXISTS(SELECT 1 FROM intelligence_check_model_configs WHERE group_id=$1 AND id<>$4 AND btrim(config->>'model')=$2)`,
		c.GroupID, c.Model, addModel || c.ProbeID > 0, c.ProbeID).Scan(&primaryExists, &extraCount, &duplicate)
	if err != nil {
		return c, err
	}
	if duplicate {
		return c, ErrDuplicateModel
	}
	if addModel && !primaryExists {
		return c, ErrConflict
	}
	if addModel && extraCount >= MaxGroupModels-1 {
		return c, ErrModelLimit
	}
	var version int
	probeID := c.ProbeID
	// Config updates do not revoke an in-flight lease: avoid overlapping paid calls.
	// Existing run keeps its immutable configuration snapshot; next run uses new settings.
	const assignments = ` SET config=$2,enabled=$3,interval_minutes=$4,timeout_seconds=$5,version=version+1,next_run_at=NOW()+($4::integer * INTERVAL '1 minute'),updated_at=NOW() WHERE group_id=$1 AND version=$6`
	switch {
	case addModel:
		err = tx.QueryRowContext(ctx, `INSERT INTO intelligence_check_model_configs (group_id,config,enabled,interval_minutes,timeout_seconds) VALUES($1,$2,$3,$4,$5) RETURNING version,id`, c.GroupID, raw, c.Enabled, c.IntervalMinutes, c.TimeoutSeconds).Scan(&version, &probeID)
	case c.ProbeID > 0:
		err = tx.QueryRowContext(ctx, `UPDATE intelligence_check_model_configs`+assignments+` AND id=$7 RETURNING version`, c.GroupID, raw, c.Enabled, c.IntervalMinutes, c.TimeoutSeconds, c.Version, c.ProbeID).Scan(&version)
	case c.Version == 0:
		err = tx.QueryRowContext(ctx, `INSERT INTO intelligence_check_configs (group_id,config,enabled,interval_minutes,timeout_seconds) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (group_id) DO NOTHING RETURNING version`, c.GroupID, raw, c.Enabled, c.IntervalMinutes, c.TimeoutSeconds).Scan(&version)
	default:
		err = tx.QueryRowContext(ctx, `UPDATE intelligence_check_configs`+assignments+` RETURNING version`, c.GroupID, raw, c.Enabled, c.IntervalMinutes, c.TimeoutSeconds, c.Version).Scan(&version)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrConflict
	}
	if err != nil {
		return c, err
	}
	if err := tx.Commit(); err != nil {
		return c, err
	}
	c.Version, c.ProbeID = version, probeID
	return c, nil
}

func (s *SQLStore) DeleteModel(ctx context.Context, key ProbeKey, version int) error {
	if key.GroupID <= 0 || key.ProbeID <= 0 || version <= 0 {
		return ErrInvalid
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM intelligence_check_model_configs
 WHERE group_id=$1 AND id=$2 AND version=$3 AND (lease_until IS NULL OR lease_until<NOW())`, key.GroupID, key.ProbeID, version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}

func (s *SQLStore) Claim(ctx context.Context, key ProbeKey, manual bool) (*Claim, error) {
	token := uuid.NewString()
	// Choose the oldest due check across both tables, or the exact manual target.
	// Skipping locked rows and fencing keep overlapping runs out of a probe's history.
	const claimable = eligibleGroupSQL + ` AND ($1=0 OR c.group_id=$1) AND ($3 OR (c.enabled AND c.next_run_at<=NOW())) AND (c.lease_until IS NULL OR c.lease_until<NOW())`
	const lease = ` SET lease_token=$2,lease_until=NOW()+((timeout_seconds+60)*INTERVAL '1 second'),next_run_at=NOW()+(interval_minutes*INTERVAL '1 minute')`
	row := s.DB.QueryRowContext(ctx, `WITH next_primary AS (
 SELECT c.group_id,0::bigint AS probe_id,c.next_run_at FROM intelligence_check_configs c JOIN groups g ON g.id=c.group_id
 WHERE `+claimable+` AND $4=0 ORDER BY c.next_run_at,c.group_id LIMIT 1 FOR UPDATE OF c SKIP LOCKED
 ), next_model AS (
 SELECT c.group_id,c.id AS probe_id,c.next_run_at FROM intelligence_check_model_configs c JOIN groups g ON g.id=c.group_id
 WHERE `+claimable+` AND (NOT $3 OR c.id=$4) ORDER BY c.next_run_at,c.id LIMIT 1 FOR UPDATE OF c SKIP LOCKED
 ), candidate AS (
 SELECT * FROM (SELECT * FROM next_primary UNION ALL SELECT * FROM next_model) due ORDER BY next_run_at,group_id,probe_id LIMIT 1
 ), primary_claim AS (
 UPDATE intelligence_check_configs`+lease+` WHERE group_id=(SELECT group_id FROM candidate WHERE probe_id=0)
 RETURNING config,version,group_id,0::bigint AS probe_id
 ), model_claim AS (
 UPDATE intelligence_check_model_configs`+lease+` WHERE id=(SELECT probe_id FROM candidate WHERE probe_id>0)
 RETURNING config,version,group_id,id AS probe_id
 ) SELECT * FROM primary_claim UNION ALL SELECT * FROM model_claim`, key.GroupID, token, manual, key.ProbeID)
	var raw []byte
	var version int
	if err := row.Scan(&raw, &version, &key.GroupID, &key.ProbeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConflict
		}
		return nil, err
	}
	c, err := scanConfig(raw, version, key)
	if err != nil {
		return nil, err
	}
	return &Claim{Config: c, Token: token}, nil
}

func scanConfig(raw []byte, version int, key ProbeKey) (Config, error) {
	var c Config
	err := json.Unmarshal(raw, &c)
	c.Version, c.GroupID, c.ProbeID = version, key.GroupID, key.ProbeID
	return c, err
}

func (s *SQLStore) Finish(ctx context.Context, claim *Claim, r Record) error {
	// Usage is persisted asynchronously after the completed response. Wait only
	// for this exact request/key/group, never guess from recent group traffic.
	// Additional model checks observe quality only; they neither pause nor recover accounts.
	primary := claim.Config.ProbeID == 0
	if primary && s.Protect != nil && r.RequestID != "" {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		r.AccountID = s.resolveAccount(lookupCtx, claim.Config, r.RequestID)
		cancel()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if primary {
		result, err = tx.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_token=NULL,lease_until=NULL WHERE group_id=$1 AND lease_token=$2`, claim.Config.GroupID, claim.Token)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE intelligence_check_model_configs SET lease_token=NULL,lease_until=NULL WHERE group_id=$1 AND lease_token=$2 AND id=$3`, claim.Config.GroupID, claim.Token, claim.Config.ProbeID)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if r.GroupID != claim.Config.GroupID {
		return ErrInvalid
	}
	eligible := false
	if primary && s.ProtectionEligible != nil {
		eligible, err = s.ProtectionEligible(ctx, claim.Config)
		if err != nil {
			return err
		}
	}
	if eligible && r.AccountID > 0 {
		// Lock before reading AND writing history. Results from different groups
		// must observe each other's committed protection and recovery.
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('intelligence_account_protection'))`); err != nil {
			return err
		}
		var cutoff time.Time
		if s.RecoveryCutoff != nil {
			cutoff, err = s.RecoveryCutoff(ctx, r.AccountID)
			if err != nil {
				return err
			}
		}
		// Ignore results that started before the account's latest confirmed recovery.
		if !r.CheckedAt.Before(cutoff) && r.Status == "normal" && s.Recover != nil {
			if err = s.Recover(ctx, r.AccountID); err != nil {
				return err
			}
		} else if !r.CheckedAt.Before(cutoff) && r.Status == "degraded" && s.Protect != nil {
			thresholdReached, err := s.pauseThresholdReached(ctx, tx, r.AccountID, cutoff)
			if err != nil {
				slog.Error("intelligence pause threshold check failed", "group_id", r.GroupID, "account_id", r.AccountID, "error", err)
			} else if thresholdReached {
				r.Paused, err = s.Protect(ctx, claim.Config, r.AccountID)
				if err != nil {
					slog.Error("intelligence account protection failed", "group_id", r.GroupID, "account_id", r.AccountID, "error", err)
				}
			}
		}
	}
	raw, err := json.Marshal(claim.Config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intelligence_check_runs(group_id,checked_at,duration_ms,status,answer,error,config_snapshot,account_id,request_id,paused,protection_eligible,probe_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,$10,$11,$12)`, r.GroupID, r.CheckedAt, r.DurationMS, r.Status, r.Answer, r.Error, raw, r.AccountID, r.RequestID, r.Paused, eligible, claim.Config.ProbeID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// The current result is already known to be degraded. Check only the prior
// results for this account, under the same lock used to append the current run.
func (s *SQLStore) pauseThresholdReached(ctx context.Context, tx *sql.Tx, accountID int64, cutoff time.Time) (bool, error) {
	threshold := DefaultPauseThreshold
	if s.PauseThreshold != nil {
		var err error
		threshold, err = s.PauseThreshold(ctx)
		if err != nil {
			return false, err
		}
	}
	if !ValidPauseThreshold(threshold) {
		return false, ErrInvalidPauseThreshold
	}
	if threshold == DefaultPauseThreshold {
		return true, nil
	}
	var reached bool
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*)=$3 AND COALESCE(BOOL_AND(status='degraded'),false)
 FROM (SELECT status FROM intelligence_check_runs
 WHERE account_id=$1 AND protection_eligible AND probe_id=0 AND checked_at >= $2
 ORDER BY id DESC LIMIT $3) recent`, accountID, cutoff, threshold-1).Scan(&reached)
	return reached, err
}

func (s *SQLStore) resolveAccount(ctx context.Context, c Config, requestID string) int64 {
	for {
		var id int64
		err := s.DB.QueryRowContext(ctx, `SELECT account_id FROM usage_logs WHERE request_id=$1 AND api_key_id=$2 AND group_id=$3 ORDER BY id DESC LIMIT 1`, requestID, c.APIKeyID, c.GroupID).Scan(&id)
		if err == nil {
			return id
		}
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("intelligence account attribution unavailable", "group_id", c.GroupID, "error", err)
			return 0
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			slog.Warn("intelligence account attribution timed out", "group_id", c.GroupID)
			return 0
		case <-timer.C:
		}
	}
}

const recordColumns = `id,group_id,checked_at,duration_ms,status,answer,error,config_snapshot,COALESCE(account_id,0),paused`

func (s *SQLStore) History(ctx context.Context, key ProbeKey, since time.Time, beforeID int64, limit int) ([]Record, error) {
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+recordColumns+` FROM intelligence_check_runs WHERE group_id=$1 AND probe_id=$2 AND checked_at>=$3 AND ($4=0 OR id<$4) ORDER BY id DESC LIMIT $5`, key.GroupID, key.ProbeID, since, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SQLStore) Record(ctx context.Context, groupID, recordID int64, since time.Time) (Record, error) {
	return scanRecord(s.DB.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM intelligence_check_runs WHERE group_id=$1 AND id=$2 AND checked_at>=$3`, groupID, recordID, since))
}

func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var raw []byte
	if err := row.Scan(&r.ID, &r.GroupID, &r.CheckedAt, &r.DurationMS, &r.Status, &r.Answer, &r.Error, &raw, &r.AccountID, &r.Paused); err != nil {
		return Record{}, err
	}
	err := json.Unmarshal(raw, &r.Config)
	return r, err
}

func (s *SQLStore) Timelines(ctx context.Context, configs []Config, since time.Time, limit int) (map[ProbeKey][]TimelineRecord, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	groups, probes, models := make([]int64, 0, len(configs)), make([]int64, 0, len(configs)), make([]string, 0, len(configs))
	for _, c := range configs {
		groups, probes, models = append(groups, c.GroupID), append(probes, c.ProbeID), append(models, c.Model)
	}
	// Filter by the saved model before applying the per-probe limit. A model
	// change leaves old records in admin history without relabeling them here.
	rows, err := s.DB.QueryContext(ctx, `SELECT c.group_id,c.probe_id,r.id,r.checked_at,r.duration_ms,r.status
 FROM unnest($1::bigint[],$2::bigint[],$3::text[]) AS c(group_id,probe_id,model)
 CROSS JOIN LATERAL (
   SELECT id,checked_at,duration_ms,status FROM intelligence_check_runs
   WHERE group_id=c.group_id AND probe_id=c.probe_id AND checked_at>=$4 AND config_snapshot->>'model'=c.model
   ORDER BY id DESC LIMIT $5
 ) r ORDER BY c.group_id,c.probe_id,r.id DESC`, pq.Array(groups), pq.Array(probes), pq.Array(models), since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[ProbeKey][]TimelineRecord, len(configs))
	for rows.Next() {
		var key ProbeKey
		var r TimelineRecord
		if err := rows.Scan(&key.GroupID, &key.ProbeID, &r.ID, &r.CheckedAt, &r.DurationMS, &r.Status); err != nil {
			return nil, err
		}
		r.GroupID = key.GroupID
		result[key] = append(result[key], r)
	}
	return result, rows.Err()
}

func (s *SQLStore) Prune(ctx context.Context) error {
	for {
		result, err := s.DB.ExecContext(ctx, "DELETE FROM intelligence_check_runs WHERE id IN (SELECT id FROM intelligence_check_runs WHERE checked_at<NOW()-INTERVAL '7 days' LIMIT 1000)")
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n < 1000 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// ReconcileLegacyProtection recalculates legacy pauses and persistent group
// exceptions from existing check history. It never generates a probe.
func (s *SQLStore) ReconcileLegacyProtection(ctx context.Context) error {
	return s.reconcileProtection(ctx, false)
}

// ReconcileProtectionScopes catches groups newly included by a rename while an
// account is already isolated. Reuse the attributed check and the scheduler's
// alternative-account filters instead of creating another paid probe.
func (s *SQLStore) ReconcileProtectionScopes(ctx context.Context) error {
	return s.reconcileProtection(ctx, true)
}

func (s *SQLStore) reconcileProtection(ctx context.Context, changedScopesOnly bool) error {
	if s.Protect == nil {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('intelligence_account_protection'))`); err != nil {
		return err
	}
	query := `SELECT a.id,r.status,r.config_snapshot FROM accounts a
 JOIN LATERAL (SELECT status,config_snapshot FROM intelligence_check_runs
 WHERE account_id=a.id AND protection_eligible ORDER BY id DESC LIMIT 1) r ON TRUE
 WHERE ((a.temp_unschedulable_until>NOW() AND a.temp_unschedulable_reason LIKE 'intelligence:%')
 OR a.extra->'intelligence_recovery_required'='true'::jsonb)`
	if changedScopesOnly {
		query += ` AND a.deleted_at IS NULL AND EXISTS (
 SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
 WHERE ag.account_id=a.id AND g.deleted_at IS NULL
 AND g.platform='openai' AND g.status='active' AND POSITION('不降智' IN g.name)>0
 AND NOT COALESCE(a.extra->'intelligence_protected_groups' @> jsonb_build_array(g.id),false))`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	type pending struct {
		id     int64
		status string
		config Config
	}
	var items []pending
	for rows.Next() {
		var item pending
		var raw []byte
		if err = rows.Scan(&item.id, &item.status, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &item.config); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.status == "normal" && s.Recover != nil {
			err = s.Recover(ctx, item.id)
		} else {
			_, err = s.Protect(ctx, item.config, item.id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
