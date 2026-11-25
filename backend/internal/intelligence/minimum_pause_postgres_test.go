package intelligence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntelligenceRecoveryMinimumPausePostgres(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	store := &SQLRecoveryStore{DB: db}
	require.NoError(t, store.Sync(ctx))
	var deadline time.Time
	require.NoError(t, db.QueryRow(`SELECT next_run_at FROM intelligence_recovery_queue`).Scan(&deadline))
	require.WithinDuration(t, time.Now().Add(20*time.Minute), deadline, 2*time.Second)
	// Simulate an old 3-minute queue being overdue, including a direct claim
	// that races the next Sync. Neither route may bypass the account deadline.
	_, err := db.Exec(`UPDATE intelligence_recovery_queue SET next_run_at=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	_, err = store.ClaimRecovery(ctx, DefaultConfig(79))
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, store.Sync(ctx))
	var repaired time.Time
	require.NoError(t, db.QueryRow(`SELECT next_run_at FROM intelligence_recovery_queue`).Scan(&repaired))
	require.Equal(t, deadline, repaired)
	require.NoError(t, store.Sync(ctx))
	var repeated time.Time
	require.NoError(t, db.QueryRow(`SELECT next_run_at FROM intelligence_recovery_queue`).Scan(&repeated))
	require.Equal(t, repaired, repeated, "ticks must not extend the same pause")
	// Re-isolation creates a new generation and aligns its queue with that
	// generation, even if an older queue entry was already due.
	_, err = db.Exec(`UPDATE accounts SET extra=jsonb_set(extra,'{intelligence_pause_until}',to_jsonb((NOW()+INTERVAL '40 minutes')::text))`)
	require.NoError(t, err)
	require.NoError(t, store.Sync(ctx))
	var resetStep int
	require.NoError(t, db.QueryRow(`SELECT retry_step,next_run_at FROM intelligence_recovery_queue`).Scan(&resetStep, &repeated))
	require.Zero(t, resetStep)
	require.WithinDuration(t, time.Now().Add(40*time.Minute), repeated, 2*time.Second)
	// A long probe still waits from completion, not from its old start time.
	advanceRecoveryDue(t, db)
	claim, err := store.ClaimRecovery(ctx, DefaultConfig(79))
	require.NoError(t, err)
	claim.StartedAt = time.Now().Add(-15 * time.Minute)
	require.NoError(t, store.FinishRecovery(ctx, claim, Record{Status: "degraded"}))
	var minutes float64
	require.NoError(t, db.QueryRow(`SELECT EXTRACT(EPOCH FROM(next_run_at-NOW()))/60 FROM intelligence_recovery_queue`).Scan(&minutes))
	require.InDelta(t, 30, minutes, .02)
	// Even corrupt/stale short retry state cannot start a new probe early.
	_, err = db.Exec(`UPDATE intelligence_recovery_queue SET next_run_at=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	_, err = store.ClaimRecovery(ctx, DefaultConfig(79))
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, store.Sync(ctx))
	require.NoError(t, db.QueryRow(`SELECT EXTRACT(EPOCH FROM(next_run_at-NOW()))/60 FROM intelligence_recovery_queue`).Scan(&minutes))
	require.InDelta(t, 20, minutes, .02)
	t.Log("first and replacement generations respect pause_until; legacy queue cannot probe early; slow degraded probe waits 30m after completion; legacy retry is at least 20m")
}

func TestIntelligenceRecoveryIntervalMinimum(t *testing.T) {
	for _, tc := range []struct{ step, minutes int }{{-1, 20}, {0, 20}, {1, 30}, {2, 40}, {3, 50}, {7, 50}, {100, 50}} {
		require.Equal(t, time.Duration(tc.minutes)*time.Minute, RecoveryInterval(tc.step))
	}
}

func TestIntelligenceStaleResultCannotRePausePostgres(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(`DROP TABLE intelligence_check_runs;
 CREATE TABLE intelligence_check_configs(group_id BIGINT PRIMARY KEY,lease_token TEXT,lease_until TIMESTAMPTZ);
 CREATE TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,group_id BIGINT,checked_at TIMESTAMPTZ,duration_ms BIGINT,status TEXT,answer TEXT,error TEXT,config_snapshot JSONB,account_id BIGINT,request_id TEXT,paused BOOLEAN,protection_eligible BOOLEAN,probe_id BIGINT NOT NULL DEFAULT 0);
 INSERT INTO intelligence_check_configs VALUES(79,'owned',NULL)`)
	require.NoError(t, err)
	pauses, recoveries := 0, 0
	cutoff := time.Now()
	store := &SQLStore{DB: db, ProtectionEligible: func(context.Context, Config) (bool, error) { return true, nil }, RecoveryCutoff: func(context.Context, int64) (time.Time, error) { return cutoff, nil }, Protect: func(context.Context, Config, int64) (bool, error) { pauses++; return true, nil }, Recover: func(context.Context, int64) error { recoveries++; return nil }}
	for _, status := range []string{"degraded", "normal"} {
		_, err = db.Exec(`UPDATE intelligence_check_configs SET lease_token='owned'`)
		require.NoError(t, err)
		require.NoError(t, store.Finish(ctx, &Claim{Config: DefaultConfig(79), Token: "owned"}, Record{GroupID: 79, AccountID: 1, Status: status, CheckedAt: cutoff.Add(-time.Minute)}))
	}
	require.Zero(t, pauses)
	require.Zero(t, recoveries)
	t.Log("late pre-recovery degraded/normal results are recorded without changing current isolation")
}
