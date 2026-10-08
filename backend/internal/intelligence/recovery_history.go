package intelligence

import (
	"context"
	"time"
)

type RecoveryRecord struct {
	ID              int64      `json:"id"`
	AccountID       int64      `json:"account_id"`
	AccountName     string     `json:"account_name"`
	GroupID         int64      `json:"group_id"`
	GroupName       string     `json:"group_name"`
	Model           string     `json:"model"`
	ReasoningEffort string     `json:"reasoning_effort"`
	Protocol        string     `json:"protocol"`
	RetryStep       int        `json:"retry_step"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	DurationMS      int64      `json:"duration_ms"`
	Status          string     `json:"status"`
	Outcome         string     `json:"outcome"`
}

type RecoveryQueueEntry struct {
	AccountID       int64      `json:"account_id"`
	AccountName     string     `json:"account_name"`
	RetryStep       int        `json:"retry_step"`
	IntervalMinutes int        `json:"interval_minutes"`
	NextRunAt       time.Time  `json:"next_run_at"`
	Running         bool       `json:"running"`
	LastCheckedAt   *time.Time `json:"last_checked_at"`
	LastStatus      *string    `json:"last_status"`
}

type RecoveryAdminStore interface {
	RecoveryHistory(context.Context, int64, int) ([]RecoveryRecord, error)
	RecoveryQueue(context.Context) ([]RecoveryQueueEntry, error)
}

func (s *SQLRecoveryStore) RecoveryHistory(ctx context.Context, before int64, limit int) ([]RecoveryRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,account_id,account_name,group_id,group_name,model,reasoning_effort,protocol,retry_step,started_at,finished_at,duration_ms,status,outcome
 FROM intelligence_recovery_runs WHERE started_at>=NOW()-INTERVAL '7 days' AND ($1::bigint=0 OR id<$1) ORDER BY id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RecoveryRecord{}
	for rows.Next() {
		var r RecoveryRecord
		if err = rows.Scan(&r.ID, &r.AccountID, &r.AccountName, &r.GroupID, &r.GroupName, &r.Model, &r.ReasoningEffort, &r.Protocol, &r.RetryStep, &r.StartedAt, &r.FinishedAt, &r.DurationMS, &r.Status, &r.Outcome); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

func (s *SQLRecoveryStore) RecoveryQueue(ctx context.Context) ([]RecoveryQueueEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT q.account_id,a.name,q.retry_step,q.next_run_at,COALESCE(q.lease_until>NOW(),false),q.last_checked_at,q.last_status
 FROM intelligence_recovery_queue q JOIN accounts a ON a.id=q.account_id WHERE a.deleted_at IS NULL AND a.extra->>'intelligence_recovery_required'='true' AND a.extra->>'intelligence_pause_until'=q.generation ORDER BY q.next_run_at,q.account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RecoveryQueueEntry{}
	for rows.Next() {
		var q RecoveryQueueEntry
		if err = rows.Scan(&q.AccountID, &q.AccountName, &q.RetryStep, &q.NextRunAt, &q.Running, &q.LastCheckedAt, &q.LastStatus); err != nil {
			return nil, err
		}
		q.IntervalMinutes = int(RecoveryInterval(q.RetryStep) / time.Minute)
		items = append(items, q)
	}
	return items, rows.Err()
}
