package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type schedulerTestLeaderLock struct {
	mu     sync.Mutex
	owners map[string]string
	err    error
}

func (c *schedulerTestLeaderLock) TryAcquireLeaderLock(_ context.Context, key, owner string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return false, c.err
	}
	if c.owners == nil {
		c.owners = map[string]string{}
	}
	if c.owners[key] != "" {
		return false, nil
	}
	c.owners[key] = owner
	return true, nil
}
func (c *schedulerTestLeaderLock) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owners[key] == owner {
		delete(c.owners, key)
	}
	return nil
}

type schedulerFleetTestCache struct {
	*outboxCleanupCache
	completedAt                     time.Time
	cadenceReadErr, cadenceWriteErr error
	started, proceed                chan struct{}
	updates                         int
}

type schedulerFleetAccountRepo struct{ AccountRepository }

func (*schedulerFleetAccountRepo) ListSchedulableByPlatform(context.Context, string) ([]Account, error) {
	return nil, nil
}
func (*schedulerFleetAccountRepo) ListSchedulableByPlatforms(context.Context, []string) ([]Account, error) {
	return nil, nil
}

func (c *schedulerFleetTestCache) GetFullRebuildCompletedAt(context.Context) (time.Time, error) {
	return c.completedAt, c.cadenceReadErr
}
func (c *schedulerFleetTestCache) SetFullRebuildCompletedAt(_ context.Context, at time.Time) error {
	if c.cadenceWriteErr != nil {
		return c.cadenceWriteErr
	}
	c.completedAt = at
	return nil
}
func (c *schedulerFleetTestCache) UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	c.updates++
	if c.started != nil {
		close(c.started)
		<-c.proceed
		c.started = nil
	}
	return c.updateErr
}

func TestSchedulerFleetOutboxSingleConsumerAndRetry(t *testing.T) {
	for _, failFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "one consumer", true: "failed handler is retryable"}[failFirst], func(t *testing.T) {
			cache := &schedulerFleetTestCache{outboxCleanupCache: &outboxCleanupCache{}, started: make(chan struct{}), proceed: make(chan struct{})}
			if failFirst {
				cache.updateErr = errors.New("snapshot update failed")
			}
			repo := &outboxCleanupRepo{events: []SchedulerOutboxEvent{{ID: 1, EventType: SchedulerOutboxEventAccountLastUsed, Payload: map[string]any{"last_used": map[string]any{"7": time.Now().Unix()}}}}}
			locks := &schedulerTestLeaderLock{}
			a := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)
			b := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)
			a.SetLeaderLock(locks, nil)
			b.SetLeaderLock(locks, nil)
			done := make(chan struct{})
			go func() { a.pollOutbox(); close(done) }()
			select {
			case <-cache.started:
			case <-time.After(time.Second):
				t.Fatal("first consumer did not start")
			}
			b.pollOutbox() // Must not even read the shared watermark while A processes.
			require.Equal(t, 1, cache.updates)
			require.Zero(t, cache.watermark)
			close(cache.proceed)
			<-done
			if failFirst {
				require.Zero(t, cache.watermark)
				cache.updateErr = nil
				b.pollOutbox()
				require.Equal(t, 2, cache.updates)
			} else {
				b.pollOutbox()
				require.Equal(t, 1, cache.updates)
			}
			require.EqualValues(t, 1, cache.watermark)
			require.Empty(t, locks.owners)
		})
	}
}

func TestSchedulerFleetPeriodicCadenceAcrossStartPhases(t *testing.T) {
	cache := &schedulerFleetTestCache{outboxCleanupCache: &outboxCleanupCache{}}
	locks := &schedulerTestLeaderLock{}
	newService := func() *SchedulerSnapshotService {
		svc := NewSchedulerSnapshotService(cache, nil, &schedulerFleetAccountRepo{}, nil, &config.Config{RunMode: config.RunModeSimple})
		svc.SetLeaderLock(locks, nil)
		return svc
	}
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for _, offset := range []time.Duration{0, 30 * time.Second, 80 * time.Second, 190 * time.Second, 299 * time.Second} {
		require.NoError(t, newService().runPeriodicFullRebuildAt(300*time.Second, func() time.Time { return base.Add(offset) }))
	}
	require.Equal(t, 1, cache.listBucketCalls)
	require.Equal(t, base, cache.completedAt)
	require.NoError(t, newService().runPeriodicFullRebuildAt(300*time.Second, func() time.Time { return base.Add(300 * time.Second) }))
	require.Equal(t, 2, cache.listBucketCalls)
	// Administrative/outbox/startup rebuilds do not get suppressed by the cadence.
	require.NoError(t, newService().triggerFullRebuild("outbox"))
	require.NoError(t, newService().triggerFullRebuild("manual"))
	newService().runInitialRebuild()
	require.Equal(t, 5, cache.listBucketCalls)
}

func TestSchedulerFleetPeriodicFailureRetriesWithinCadence(t *testing.T) {
	for _, failure := range []string{"rebuild", "read", "write"} {
		t.Run(failure, func(t *testing.T) {
			cache := &schedulerFleetTestCache{outboxCleanupCache: &outboxCleanupCache{}}
			wantErr := errors.New("temporary failure")
			switch failure {
			case "rebuild":
				cache.listBucketErr = wantErr
			case "read":
				cache.cadenceReadErr = wantErr
			case "write":
				cache.cadenceWriteErr = wantErr
			}
			locks := &schedulerTestLeaderLock{}
			newService := func() *SchedulerSnapshotService {
				s := NewSchedulerSnapshotService(cache, nil, &schedulerFleetAccountRepo{}, nil, &config.Config{RunMode: config.RunModeSimple})
				s.SetLeaderLock(locks, nil)
				return s
			}
			base := time.Now().UTC()
			require.ErrorIs(t, newService().runPeriodicFullRebuildAt(5*time.Minute, func() time.Time { return base }), wantErr)
			require.True(t, cache.completedAt.IsZero())
			require.Empty(t, locks.owners)
			cache.listBucketErr, cache.cadenceReadErr, cache.cadenceWriteErr = nil, nil, nil
			require.NoError(t, newService().runPeriodicFullRebuildAt(5*time.Minute, func() time.Time { return base.Add(time.Second) }))
			require.Equal(t, base.Add(time.Second), cache.completedAt)
		})
	}
}

func TestSchedulerFleetRedisAndPostgresCoordination(t *testing.T) {
	for _, redisFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "both fences", true: "postgres fallback"}[redisFails], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			locks := &schedulerTestLeaderLock{}
			if redisFails {
				locks.err = errors.New("Redis unavailable")
			}
			svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
			svc.SetLeaderLock(locks, db)
			mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(hashAdvisoryLockID(schedulerOutboxConsumerLockKey)).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			mock.ExpectExec(`SELECT pg_advisory_unlock`).WillReturnResult(sqlmock.NewResult(0, 1))
			release, acquired, err := svc.tryAcquireSchedulerLeaderLock(context.Background(), schedulerOutboxConsumerLockKey)
			require.NoError(t, err)
			require.True(t, acquired)
			release()
			release() // Cleanup is idempotent, including the SQL connection.
			require.Empty(t, locks.owners)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSchedulerFleetLockFailureDoesNotProcessOrAdvance(t *testing.T) {
	cache := &schedulerFleetTestCache{outboxCleanupCache: &outboxCleanupCache{}}
	locks := &schedulerTestLeaderLock{err: errors.New("Redis unavailable")}
	svc := NewSchedulerSnapshotService(cache, &outboxCleanupRepo{}, nil, nil, nil)
	svc.SetLeaderLock(locks, nil)
	svc.pollOutbox()
	require.Zero(t, cache.updates)
	require.Empty(t, cache.setWatermarks)
}

func TestSchedulerFleetPostgresContentionReleasesRedisLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	locks := &schedulerTestLeaderLock{}
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	svc.SetLeaderLock(locks, db)
	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	_, acquired, err := svc.tryAcquireSchedulerLeaderLock(context.Background(), schedulerOutboxConsumerLockKey)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Empty(t, locks.owners)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerFleetPostgresUnlockFailureDiscardsConnection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(`SELECT pg_advisory_unlock`).WillReturnError(errors.New("connection lost"))
	mock.ExpectClose()
	release, acquired, err := tryAcquireDBAdvisoryLockWithError(context.Background(), db, 1)
	require.NoError(t, err)
	require.True(t, acquired)
	release()
	require.NoError(t, mock.ExpectationsWereMet())
	_ = db.Close()
}
