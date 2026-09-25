package service

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"testing"
)

// The fence must expire on the database server, even when a TCP relay keeps
// an abandoned client session alive. Redis expiry alone cannot do this.
func TestSchedulerFleetDatabaseFenceHasServerLifetime(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL idle_in_transaction_session_timeout = '180s'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET LOCAL statement_timeout = '2s'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(hashAdvisoryLockID(schedulerOutboxConsumerLockKey)).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectRollback()
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	svc.SetLeaderLock(&schedulerTestLeaderLock{}, db)
	release, acquired, err := svc.tryAcquireSchedulerLeaderLock(context.Background(), schedulerOutboxConsumerLockKey)
	require.NoError(t, err)
	require.True(t, acquired)
	release()
	require.NoError(t, mock.ExpectationsWereMet())
}
