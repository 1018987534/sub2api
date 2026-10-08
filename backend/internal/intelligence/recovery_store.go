package intelligence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var recoveryMinutes = [...]int{3, 5, 8, 10, 20, 30, 40, 50}

func RecoveryInterval(step int) time.Duration {
	if step < 0 {
		step = 0
	}
	if step >= len(recoveryMinutes) {
		step = len(recoveryMinutes) - 1
	}
	return time.Duration(recoveryMinutes[step]) * time.Minute
}

type RecoveryClaim struct {
	AccountID         int64
	Generation, Token string
	Config            Config
	StartedAt         time.Time
}

type RecoveryStore interface {
	Sync(context.Context) error
	ClaimRecovery(context.Context, Config) (*RecoveryClaim, error)
	FinishRecovery(context.Context, *RecoveryClaim, Record) error
}

// No recovery answers or customer monitor rows are stored here. Leases and retry state
// survive process restarts and fence concurrent control instances.
type SQLRecoveryStore struct {
	DB      *sql.DB
	Recover func(context.Context, int64) error
}

func (s *SQLRecoveryStore) Sync(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE intelligence_recovery_runs SET status='error',outcome='interrupted',finished_at=NOW()
 WHERE status='running' AND lease_until<NOW();
 DELETE FROM intelligence_recovery_runs WHERE started_at<NOW()-INTERVAL '7 days'`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO intelligence_recovery_queue(account_id,generation)
 SELECT id,extra->>'intelligence_pause_until' FROM accounts
 WHERE deleted_at IS NULL AND platform='openai'
 AND extra->>'intelligence_recovery_required'='true'
 AND NULLIF(extra->>'intelligence_pause_until','') IS NOT NULL
 ON CONFLICT(account_id) DO UPDATE SET generation=EXCLUDED.generation,
 retry_step=0,next_run_at=NOW()+INTERVAL '3 minutes',lease_token=NULL,lease_until=NULL,
 last_checked_at=NULL,last_status=NULL
 WHERE intelligence_recovery_queue.generation<>EXCLUDED.generation`)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM intelligence_recovery_queue q
 WHERE NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id=q.account_id AND a.deleted_at IS NULL
 AND a.extra->>'intelligence_recovery_required'='true'
 AND a.extra->>'intelligence_pause_until'=q.generation)`)
	return err
}

func (s *SQLRecoveryStore) ClaimRecovery(ctx context.Context, c Config) (*RecoveryClaim, error) {
	claim := &RecoveryClaim{Config: c, Token: uuid.NewString()}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `UPDATE intelligence_recovery_queue SET lease_token=$1,
 lease_until=NOW()+($2::integer * INTERVAL '1 second')
 WHERE account_id=(SELECT q.account_id FROM intelligence_recovery_queue q
 JOIN accounts a ON a.id=q.account_id
 WHERE q.next_run_at<=NOW() AND (q.lease_until IS NULL OR q.lease_until<NOW())
 AND a.deleted_at IS NULL AND a.status='active' AND a.schedulable AND a.platform='openai'
 AND a.extra->>'intelligence_recovery_required'='true'
 AND a.extra->>'intelligence_pause_until'=q.generation
 ORDER BY q.next_run_at,q.account_id FOR UPDATE OF q SKIP LOCKED LIMIT 1)
	 RETURNING account_id,generation,NOW()`, claim.Token, c.TimeoutSeconds+60).Scan(&claim.AccountID, &claim.Generation, &claim.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intelligence_recovery_runs(lease_token,account_id,account_name,group_id,group_name,model,reasoning_effort,protocol,retry_step,started_at,lease_until)
 SELECT q.lease_token,q.account_id,a.name,$2,COALESCE((SELECT name FROM groups WHERE id=$2),''),$3,$4,$5,q.retry_step,$6,q.lease_until
 FROM intelligence_recovery_queue q JOIN accounts a ON a.id=q.account_id WHERE q.lease_token=$1`, claim.Token, c.GroupID, c.Model, c.ReasoningEffort, c.Protocol, claim.StartedAt)
	if err != nil {
		return nil, err
	}
	return claim, tx.Commit()
}

func (s *SQLRecoveryStore) FinishRecovery(ctx context.Context, claim *RecoveryClaim, r Record) error {
	if r.Status != "normal" && r.Status != "degraded" && r.Status != "error" {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Share the monitor protection lock: a delayed normal must never clear a
	// newer isolation episode, and monitor/recovery cannot race each other.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('intelligence_account_protection'))`); err != nil {
		return err
	}
	var step int
	err = tx.QueryRowContext(ctx, `SELECT q.retry_step FROM intelligence_recovery_queue q
 JOIN accounts a ON a.id=q.account_id WHERE q.account_id=$1 AND q.generation=$2
 AND q.lease_token=$3 AND q.lease_until>NOW() AND a.deleted_at IS NULL
 AND a.extra->>'intelligence_recovery_required'='true'
 AND a.extra->>'intelligence_pause_until'=q.generation FOR UPDATE OF q`,
		claim.AccountID, claim.Generation, claim.Token).Scan(&step)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `UPDATE intelligence_recovery_runs SET status=$2,outcome='superseded',finished_at=NOW(),duration_ms=$3 WHERE lease_token=$1 AND status='running'`, claim.Token, r.Status, r.DurationMS); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if r.Status == "normal" {
		if s.Recover == nil {
			return errors.New("recovery callback unavailable")
		}
		if err = s.Recover(ctx, claim.AccountID); err != nil {
			_, recordErr := tx.ExecContext(ctx, `UPDATE intelligence_recovery_runs SET status=$2,outcome='recovery_failed',finished_at=NOW(),duration_ms=$3 WHERE lease_token=$1`, claim.Token, r.Status, r.DurationMS)
			if recordErr != nil {
				return recordErr
			}
			if recordErr = tx.Commit(); recordErr != nil {
				return recordErr
			}
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM intelligence_recovery_queue WHERE account_id=$1 AND lease_token=$2`, claim.AccountID, claim.Token)
	} else {
		if step < 7 {
			step++
		}
		_, err = tx.ExecContext(ctx, `UPDATE intelligence_recovery_queue SET retry_step=$2,
 next_run_at=GREATEST(NOW(),$6::timestamptz+($3::integer * INTERVAL '1 minute')),lease_token=NULL,lease_until=NULL,
 last_checked_at=NOW(),last_status=$4 WHERE account_id=$1 AND lease_token=$5`,
			claim.AccountID, step, int(RecoveryInterval(step)/time.Minute), r.Status, claim.Token, claim.StartedAt)
	}
	if err != nil {
		return err
	}
	outcome := "retry"
	if r.Status == "normal" {
		outcome = "recovered"
	}
	_, err = tx.ExecContext(ctx, `UPDATE intelligence_recovery_runs SET status=$2,outcome=$3,finished_at=NOW(),duration_ms=$4 WHERE lease_token=$1`, claim.Token, r.Status, outcome, r.DurationMS)
	if err != nil {
		return err
	}
	return tx.Commit()
}
