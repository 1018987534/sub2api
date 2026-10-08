package intelligence

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIntelligenceRecoveryAdminHistoryPostgres(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	store := &SQLRecoveryStore{DB: db, Recover: func(ctx context.Context, id int64) error {
		_, err := db.ExecContext(ctx, `UPDATE accounts SET extra='{}' WHERE id=$1`, id)
		return err
	}}
	require.NoError(t, store.Sync(ctx))
	queue, err := store.RecoveryQueue(ctx)
	require.NoError(t, err)
	require.Len(t, queue, 1)
	require.Equal(t, "fixture-account", queue[0].AccountName)
	require.Equal(t, 3, queue[0].IntervalMinutes)
	_, err = db.Exec(`UPDATE intelligence_recovery_queue SET next_run_at=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	claim, err := store.ClaimRecovery(ctx, DefaultConfig(5))
	require.NoError(t, err)
	rows, err := store.RecoveryHistory(ctx, 0, 25)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "running", rows[0].Status)
	require.Equal(t, "fixture-group", rows[0].GroupName)
	queue, err = store.RecoveryQueue(ctx)
	require.NoError(t, err)
	require.True(t, queue[0].Running)
	require.NoError(t, store.FinishRecovery(ctx, claim, Record{Status: "degraded", DurationMS: 1234}))
	rows, err = store.RecoveryHistory(ctx, 0, 25)
	require.NoError(t, err)
	require.Equal(t, "retry", rows[0].Outcome)
	require.EqualValues(t, 1234, rows[0].DurationMS)
	require.NotNil(t, rows[0].FinishedAt)
	queue, err = store.RecoveryQueue(ctx)
	require.NoError(t, err)
	require.False(t, queue[0].Running)
	require.Equal(t, 5, queue[0].IntervalMinutes)
	_, err = db.Exec(`UPDATE intelligence_recovery_queue SET next_run_at=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	claim, err = store.ClaimRecovery(ctx, DefaultConfig(5))
	require.NoError(t, err)
	require.NoError(t, store.FinishRecovery(ctx, claim, Record{Status: "normal", DurationMS: 2000}))
	rows, err = store.RecoveryHistory(ctx, 0, 25)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "recovered", rows[0].Outcome)
	require.EqualValues(t, 1, rows[0].AccountID)
	earlier, err := store.RecoveryHistory(ctx, rows[0].ID, 25)
	require.NoError(t, err)
	require.Len(t, earlier, 1)
	require.Equal(t, rows[1].ID, earlier[0].ID)
	queue, err = store.RecoveryQueue(ctx)
	require.NoError(t, err)
	require.Empty(t, queue)
	var public int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM intelligence_check_runs`).Scan(&public))
	require.Equal(t, 1, public)
}
func TestIntelligenceRecoveryHistoryInterruptionAndFailure(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	s := &SQLRecoveryStore{DB: db, Recover: func(context.Context, int64) error { return errors.New("fixture failure") }}
	require.NoError(t, s.Sync(ctx))
	_, err := db.Exec(`UPDATE intelligence_recovery_queue SET next_run_at=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	claim, err := s.ClaimRecovery(ctx, DefaultConfig(5))
	require.NoError(t, err)
	require.ErrorContains(t, s.FinishRecovery(ctx, claim, Record{Status: "normal"}), "fixture failure")
	rows, err := s.RecoveryHistory(ctx, 0, 25)
	require.NoError(t, err)
	require.Equal(t, "recovery_failed", rows[0].Outcome)
	_, err = db.Exec(`UPDATE intelligence_recovery_queue SET lease_until=NOW()-INTERVAL '1 minute'; UPDATE intelligence_recovery_runs SET lease_until=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	claim, err = s.ClaimRecovery(ctx, DefaultConfig(5))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE intelligence_recovery_runs SET lease_until=NOW()-INTERVAL '1 minute' WHERE status='running'`)
	require.NoError(t, err)
	require.NoError(t, s.Sync(ctx))
	rows, err = s.RecoveryHistory(ctx, 0, 25)
	require.NoError(t, err)
	require.Equal(t, "interrupted", rows[0].Outcome)
}
