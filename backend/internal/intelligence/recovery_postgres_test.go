package intelligence

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func recoveryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("INTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "recovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		admin.Close()
	})
	_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT NOT NULL DEFAULT 'fixture-account',platform TEXT DEFAULT 'openai',status TEXT DEFAULT 'active',
 schedulable BOOLEAN DEFAULT true,deleted_at TIMESTAMPTZ,extra JSONB);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT); INSERT INTO groups VALUES(5,'fixture-group'),(79,'protected-group');
 CREATE TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,status TEXT);
 INSERT INTO accounts(id,extra) VALUES(1,jsonb_build_object('intelligence_pause_until',(NOW()+INTERVAL '20 minutes')::text,'intelligence_recovery_required',true));
 INSERT INTO intelligence_check_runs(status) VALUES('degraded');`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/245_intelligence_recovery_queue.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	// Actual migration is rerunnable.
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	historyMigration, err := os.ReadFile("../../migrations/246_intelligence_recovery_history.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(historyMigration))
	require.NoError(t, err)
	_, err = db.Exec(string(historyMigration))
	require.NoError(t, err)
	return db
}

// Advance the persisted deadline instead of sleeping for a real 20-minute pause.
func advanceRecoveryDue(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`UPDATE accounts SET extra=jsonb_set(extra,'{intelligence_pause_until}','"2000-01-01T00:00:00Z"') WHERE extra->>'intelligence_recovery_required'='true';
 UPDATE intelligence_recovery_queue q SET generation=a.extra->>'intelligence_pause_until',next_run_at=NOW()-INTERVAL '1 minute',last_checked_at=NOW()-INTERVAL '1 hour'
 FROM accounts a WHERE a.id=q.account_id`)
	require.NoError(t, err)
}

func TestIntelligenceRecoveryPostgres(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	recovered := 0
	store := &SQLRecoveryStore{DB: db, Recover: func(ctx context.Context, id int64) error {
		recovered++
		_, err := db.ExecContext(ctx, `UPDATE accounts SET extra='{}' WHERE id=$1`, id)
		return err
	}}
	require.NoError(t, store.Sync(ctx))
	var minutes float64
	require.NoError(t, db.QueryRow(`SELECT EXTRACT(EPOCH FROM(next_run_at-NOW()))/60 FROM intelligence_recovery_queue`).Scan(&minutes))
	require.InDelta(t, 20, minutes, .02)
	c := DefaultConfig(79)
	_, err := store.ClaimRecovery(ctx, c)
	require.ErrorIs(t, err, ErrConflict)
	for _, expected := range []int{30, 40, 50, 50, 50} {
		advanceRecoveryDue(t, db)
		// A new store represents restarting the control process; retry progress persists.
		store = &SQLRecoveryStore{DB: db, Recover: store.Recover}
		claim, err := store.ClaimRecovery(ctx, c)
		require.NoError(t, err)
		_, err = store.ClaimRecovery(ctx, c)
		require.ErrorIs(t, err, ErrConflict)
		require.NoError(t, store.FinishRecovery(ctx, claim, Record{Status: "degraded"}))
		require.NoError(t, db.QueryRow(`SELECT EXTRACT(EPOCH FROM(next_run_at-NOW()))/60 FROM intelligence_recovery_queue`).Scan(&minutes))
		require.InDelta(t, expected, minutes, .02)
		require.ErrorIs(t, store.FinishRecovery(ctx, claim, Record{Status: "normal"}), ErrConflict)
		t.Logf("degraded retry=%dm restart=preserved duplicate=blocked", expected)
	}
	advanceRecoveryDue(t, db)
	claim, err := store.ClaimRecovery(ctx, c)
	require.NoError(t, err)
	require.NoError(t, store.FinishRecovery(ctx, claim, Record{Status: "normal"}))
	require.Equal(t, 1, recovered)
	require.NoError(t, store.Sync(ctx))
	var queue, history int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM intelligence_recovery_queue`).Scan(&queue))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM intelligence_check_runs`).Scan(&history))
	require.Zero(t, queue)
	require.Equal(t, 1, history)
	t.Log("one normal: isolation cleared immediately; queue removed; monitor history=1 unchanged")
}

func TestIntelligenceRecoveryFencesAndParallelLeases(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	recovered := 0
	store := &SQLRecoveryStore{DB: db, Recover: func(context.Context, int64) error { recovered++; return nil }}
	require.NoError(t, store.Sync(ctx))
	advanceRecoveryDue(t, db)
	c := DefaultConfig(5)
	var wg sync.WaitGroup
	claims := make(chan *RecoveryClaim, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, err := store.ClaimRecovery(ctx, c)
			if err == nil {
				claims <- claim
			} else {
				failures <- err
			}
		}()
	}
	wg.Wait()
	require.Len(t, claims, 1)
	require.Len(t, failures, 7)
	close(failures)
	for err := range failures {
		require.ErrorIs(t, err, ErrConflict)
	}
	stale := <-claims
	_, err := db.Exec(`UPDATE accounts SET extra=jsonb_set(extra,'{intelligence_pause_until}','"2099-02-01T00:00:00Z"') WHERE id=1`)
	require.NoError(t, err)
	require.ErrorIs(t, store.FinishRecovery(ctx, stale, Record{Status: "normal"}), ErrConflict)
	require.NoError(t, store.Sync(ctx))
	require.ErrorIs(t, store.FinishRecovery(ctx, stale, Record{Status: "normal"}), ErrConflict)
	require.Zero(t, recovered)
	advanceRecoveryDue(t, db)
	old, err := store.ClaimRecovery(ctx, c)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE intelligence_recovery_queue SET lease_until=NOW()-INTERVAL '1 second'`)
	require.NoError(t, err)
	fresh, err := store.ClaimRecovery(ctx, c)
	require.NoError(t, err)
	require.NotEqual(t, old.Token, fresh.Token)
	require.ErrorIs(t, store.FinishRecovery(ctx, old, Record{Status: "normal"}), ErrConflict)
	require.NoError(t, store.FinishRecovery(ctx, fresh, Record{Status: "error"}))
	_, err = db.Exec(`UPDATE accounts SET extra='{}' WHERE id=1`)
	require.NoError(t, err)
	require.NoError(t, store.Sync(ctx))
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM intelligence_recovery_queue`).Scan(&n))
	require.Zero(t, n)
	t.Log("8 concurrent claims=1 upstream lease; stale generation/expired lease cannot recover; group reset removes queue")
}

func TestIntelligenceSingleDegradedAfterRecovery(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(`DROP TABLE intelligence_check_runs;
 CREATE TABLE intelligence_check_configs(group_id BIGINT PRIMARY KEY,lease_token TEXT,lease_until TIMESTAMPTZ);
 CREATE TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,group_id BIGINT,checked_at TIMESTAMPTZ,duration_ms BIGINT,status TEXT,answer TEXT,error TEXT,config_snapshot JSONB,account_id BIGINT,request_id TEXT,paused BOOLEAN,protection_eligible BOOLEAN,probe_id BIGINT NOT NULL DEFAULT 0);
 INSERT INTO intelligence_check_configs VALUES(79,NULL,NULL);
 INSERT INTO intelligence_check_runs(group_id,account_id,checked_at,status,protection_eligible) VALUES(79,1,NOW()-INTERVAL '1 hour','degraded',true);`)
	require.NoError(t, err)
	cutoff := time.Now()
	pauses := 0
	s := &SQLStore{DB: db,
		ProtectionEligible: func(context.Context, Config) (bool, error) { return true, nil },
		RecoveryCutoff:     func(context.Context, int64) (time.Time, error) { return cutoff, nil },
		Protect:            func(context.Context, Config, int64) (bool, error) { pauses++; return true, nil },
	}
	for i := 0; i < 2; i++ {
		_, err = db.Exec(`UPDATE intelligence_check_configs SET lease_token='owned'`)
		require.NoError(t, err)
		require.NoError(t, s.Finish(ctx, &Claim{Config: DefaultConfig(79), Token: "owned"}, Record{GroupID: 79, AccountID: 1, Status: "degraded", CheckedAt: time.Now()}))
		require.Equal(t, i+1, pauses)
	}
	var normal, total int
	require.NoError(t, db.QueryRow(`SELECT count(*) FILTER(WHERE status='normal'),count(*) FROM intelligence_check_runs`).Scan(&normal, &total))
	require.Zero(t, normal)
	require.Equal(t, 3, total)
	t.Log("internal normal is invisible; each new degraded result immediately pauses")
}
