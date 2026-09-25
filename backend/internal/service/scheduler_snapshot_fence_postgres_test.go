package service

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestSchedulerFleetFenceSetupFailureReleasesRedis(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL idle_in_transaction_session_timeout").WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()
	locks := &schedulerTestLeaderLock{}
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	svc.SetLeaderLock(locks, db)
	_, acquired, err := svc.tryAcquireSchedulerLeaderLock(context.Background(), schedulerOutboxConsumerLockKey)
	require.Error(t, err)
	require.False(t, acquired)
	require.Empty(t, locks.owners)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Opt-in only: use a disposable PostgreSQL database, never a production DSN.
func TestSchedulerFleetFenceRealPostgres(t *testing.T) {
	dsn := os.Getenv("SCHEDULER_FENCE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SCHEDULER_FENCE_TEST_DSN to a disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	key := hashAdvisoryLockID("scheduler:test:fence")
	old, err := db.Conn(ctx)
	require.NoError(t, err)
	defer old.Close()
	var acquired bool
	require.NoError(t, old.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired))
	require.True(t, acquired)
	_, acquired, err = tryAcquireSchedulerDBFence(ctx, db, key)
	require.NoError(t, err)
	require.False(t, acquired, "must conflict with old session locks during rolling upgrade")
	_, err = old.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key)
	require.NoError(t, err)
	workCtx, workCancel := context.WithCancel(ctx)
	defer workCancel()
	release, acquired, err := tryAcquireSchedulerDBFence(workCtx, db, key)
	require.NoError(t, err)
	require.True(t, acquired)
	defer release()
	time.Sleep(2100 * time.Millisecond)
	require.NoError(t, old.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired))
	require.False(t, acquired, "fence must survive acquisition context cancellation")
	workCancel()
	require.Eventually(t, func() bool {
		err = old.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired)
		return err == nil && acquired
	}, 3*time.Second, 20*time.Millisecond)
	_, err = old.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key)
	require.NoError(t, err)
	release()
	release()
	// Simulate an abandoned open connection. Do not send rollback or disconnect:
	// the server alone must release an idle transaction's advisory lock.
	orphan, err := db.Conn(ctx)
	require.NoError(t, err)
	defer orphan.Close()
	tx, err := orphan.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "SET LOCAL idle_in_transaction_session_timeout = '500ms'")
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock($1)", key).Scan(&acquired))
	require.True(t, acquired)
	require.NoError(t, old.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired))
	require.False(t, acquired)
	require.Eventually(t, func() bool {
		err = old.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired)
		return err == nil && acquired
	}, 3*time.Second, 20*time.Millisecond)
	_, err = old.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key)
	require.NoError(t, err)
	t.Log("verified rolling session/transaction exclusion, retained lease, work cancellation, idempotent release, server-only orphan expiry")
}
