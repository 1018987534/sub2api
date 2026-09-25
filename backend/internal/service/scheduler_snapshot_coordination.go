package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	schedulerOutboxConsumerLockKey  = "scheduler:outbox:consumer"
	schedulerPeriodicRebuildLockKey = "scheduler:full-rebuild:periodic"
	schedulerCoordinationLockTTL    = 3 * time.Minute
)

type schedulerFullRebuildCadenceStore interface {
	GetFullRebuildCompletedAt(context.Context) (time.Time, error)
	SetFullRebuildCompletedAt(context.Context, time.Time) error
}

func (s *SchedulerSnapshotService) SetLeaderLock(cache LeaderLockCache, db *sql.DB) {
	s.leaderLockCache, s.leaderLockDB = cache, db
}

// All production runners take the same PostgreSQL fence as well as the Redis
// lease. This prevents split leadership when only one node loses Redis access
// and falls back to PostgreSQL while a peer still holds the Redis lease.
// The unique owner also prevents an expired holder from releasing a newer lease.
func (s *SchedulerSnapshotService) tryAcquireSchedulerLeaderLock(ctx context.Context, key string) (func(), bool, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	redisRelease := func() {}
	if s.leaderLockCache != nil {
		owner := uuid.NewString()
		acquired, err := s.leaderLockCache.TryAcquireLeaderLock(lockCtx, key, owner, schedulerCoordinationLockTTL)
		if err == nil && !acquired {
			return nil, false, nil
		}
		if err != nil && s.leaderLockDB == nil {
			return nil, false, err
		}
		if err == nil {
			redisRelease = func() {
				releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer releaseCancel()
				_ = s.leaderLockCache.ReleaseLeaderLock(releaseCtx, key, owner)
			}
		}
	}
	if s.leaderLockDB != nil {
		// Give the fallback its own timeout after a Redis timeout exhausted lockCtx.
		dbRelease, acquired, err := tryAcquireSchedulerDBFence(ctx, s.leaderLockDB, hashAdvisoryLockID(key))
		if err != nil || !acquired {
			redisRelease()
			return nil, false, err
		}
		return sync.OnceFunc(func() { dbRelease(); redisRelease() }), true, nil
	}
	// Unit fixtures without either backend retain their in-process behavior.
	return sync.OnceFunc(redisRelease), true, nil
}

// Use the SAME advisory-lock namespace as older nodes during rolling releases.
// Unlike a session lock, this transaction fence has a server-enforced lifetime:
// an abandoned connection behind a TCP relay cannot block the fleet forever.
// Production work is bounded to two minutes, below this three-minute lease.
func tryAcquireSchedulerDBFence(ctx context.Context, db *sql.DB, lockID int64) (func(), bool, error) {
	leaseCtx, leaseCancel := context.WithTimeout(ctx, schedulerCoordinationLockTTL)
	acquireCtx, acquireCancel := context.WithTimeout(leaseCtx, 2*time.Second)
	defer acquireCancel()
	conn, err := db.Conn(acquireCtx)
	if err != nil {
		leaseCancel()
		return nil, false, fmt.Errorf("open scheduler fence connection: %w", err)
	}
	// Begin with the work/lease context, NOT acquireCtx, which is canceled when
	// acquisition returns and would otherwise immediately release the fence.
	tx, err := conn.BeginTx(leaseCtx, nil)
	if err != nil {
		leaseCancel()
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return nil, false, fmt.Errorf("begin scheduler fence: %w", err)
	}
	release := sync.OnceFunc(func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		leaseCancel()
		_ = conn.Close()
	})
	for _, query := range []string{
		"SET LOCAL idle_in_transaction_session_timeout = '180s'",
		"SET LOCAL statement_timeout = '2s'",
	} {
		if _, err := tx.ExecContext(acquireCtx, query); err != nil {
			release()
			return nil, false, fmt.Errorf("configure scheduler fence lifetime: %w", err)
		}
	}
	var acquired bool
	if err := tx.QueryRowContext(acquireCtx, "SELECT pg_try_advisory_xact_lock($1)", lockID).Scan(&acquired); err != nil {
		release()
		return nil, false, fmt.Errorf("acquire scheduler transaction fence: %w", err)
	}
	if !acquired {
		release()
		return nil, false, nil
	}
	return release, true, nil
}

func (s *SchedulerSnapshotService) runPeriodicFullRebuild(interval time.Duration) error {
	return s.runPeriodicFullRebuildAt(interval, time.Now)
}

func (s *SchedulerSnapshotService) runPeriodicFullRebuildAt(interval time.Duration, now func() time.Time) error {
	if interval <= 0 {
		return nil
	}
	store, ok := s.cache.(schedulerFullRebuildCadenceStore)
	if !ok {
		if s.leaderLockCache != nil || s.leaderLockDB != nil {
			return errors.New("scheduler full rebuild cadence store unavailable")
		}
		return s.triggerFullRebuild("interval")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	release, acquired, err := s.tryAcquireSchedulerLeaderLock(ctx, schedulerPeriodicRebuildLockKey)
	if err != nil || !acquired {
		return err
	}
	defer release()
	completedAt, err := store.GetFullRebuildCompletedAt(ctx)
	if err != nil {
		return err
	}
	// This is a rolling fleet cadence measured from successful completion, not
	// a per-process ticker or a wall-clock bucket that allows boundary bursts.
	if !completedAt.IsZero() && now().Before(completedAt.Add(interval)) {
		return nil
	}
	if err := s.triggerFullRebuildContext(ctx, "interval"); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	completedAt = now().UTC()
	if err := store.SetFullRebuildCompletedAt(ctx, completedAt); err != nil {
		return err
	}
	logger.LegacyPrintf("service.scheduler_snapshot", "[Scheduler] fleet periodic rebuild completed: at=%s interval=%s", completedAt.Format(time.RFC3339Nano), interval)
	return nil
}
