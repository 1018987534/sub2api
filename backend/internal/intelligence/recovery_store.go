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

// No recovery answers or monitor rows are stored here. Leases and retry state
// survive process restarts and fence concurrent control instances.
type SQLRecoveryStore struct {
	DB      *sql.DB
	Recover func(context.Context, int64) error
}

func (s *SQLRecoveryStore) Sync(ctx context.Context) error {
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
	err := s.DB.QueryRowContext(ctx, `UPDATE intelligence_recovery_queue SET lease_token=$1,
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
	return claim, err
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
	return tx.Commit()
}
