package intelligence

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntelligencePauseThresholdPostgres(t *testing.T) {
	db := recoveryTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(`DROP TABLE intelligence_check_runs;
 CREATE TABLE intelligence_check_configs(group_id BIGINT PRIMARY KEY,lease_token TEXT,lease_until TIMESTAMPTZ);
 CREATE TABLE intelligence_check_model_configs(id BIGINT PRIMARY KEY,group_id BIGINT,lease_token TEXT,lease_until TIMESTAMPTZ);
 CREATE TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,group_id BIGINT,checked_at TIMESTAMPTZ,duration_ms BIGINT,status TEXT,answer TEXT,error TEXT,config_snapshot JSONB,account_id BIGINT,request_id TEXT,paused BOOLEAN,protection_eligible BOOLEAN,probe_id BIGINT NOT NULL DEFAULT 0);
 INSERT INTO intelligence_check_configs(group_id) VALUES (5),(79),(107);
 INSERT INTO intelligence_check_model_configs(id,group_id) VALUES (8,79);`)
	require.NoError(t, err)

	threshold := 3
	var pauses atomic.Int32
	var recovered []int64
	var cutoff time.Time
	store := &SQLStore{
		DB: db,
		PauseThreshold: func(context.Context) (int, error) {
			return threshold, nil
		},
		ProtectionEligible: func(_ context.Context, c Config) (bool, error) { return c.GroupID != 107, nil },
		Protect: func(context.Context, Config, int64) (bool, error) {
			pauses.Add(1)
			return true, nil
		},
		Recover: func(_ context.Context, accountID int64) error {
			recovered = append(recovered, accountID)
			return nil
		},
		RecoveryCutoff: func(_ context.Context, accountID int64) (time.Time, error) {
			if accountID == 3 {
				return cutoff, nil
			}
			return time.Time{}, nil
		},
	}
	claim := func(group, probe int64) *Claim {
		t.Helper()
		c := DefaultConfig(group)
		c.ProbeID = probe
		if probe == 0 {
			_, err := db.Exec(`UPDATE intelligence_check_configs SET lease_token='owned' WHERE group_id=$1`, group)
			require.NoError(t, err)
		} else {
			_, err := db.Exec(`UPDATE intelligence_check_model_configs SET lease_token='owned' WHERE group_id=$1 AND id=$2`, group, probe)
			require.NoError(t, err)
		}
		return &Claim{Config: c, Token: "owned"}
	}
	finish := func(group, probe, account int64, status string, checkedAt time.Time, wantPause bool) {
		t.Helper()
		previousPauses := pauses.Load()
		require.NoError(t, store.Finish(ctx, claim(group, probe), Record{
			GroupID: group, AccountID: account, Status: status, CheckedAt: checkedAt,
		}))
		var storedStatus string
		var paused bool
		require.NoError(t, db.QueryRow(`SELECT status,paused FROM intelligence_check_runs ORDER BY id DESC LIMIT 1`).Scan(&storedStatus, &paused))
		require.Equal(t, status, storedStatus)
		require.Equal(t, wantPause, paused)
		if wantPause {
			previousPauses++
		}
		require.Equal(t, previousPauses, pauses.Load())
	}

	now := time.Now()
	finish(5, 0, 1, "degraded", now, false)
	finish(5, 0, 2, "degraded", now, false)
	finish(107, 0, 1, "normal", now, false)
	finish(79, 8, 1, "degraded", now, false)
	finish(79, 0, 1, "degraded", now, false)
	finish(79, 8, 1, "normal", now, false)
	finish(79, 0, 0, "error", now, false)
	finish(79, 0, 1, "degraded", now, true)
	require.Empty(t, recovered)

	finish(5, 0, 1, "normal", now, false)
	require.Equal(t, []int64{1}, recovered)
	finish(79, 0, 1, "degraded", now, false)
	finish(5, 0, 1, "error", now, false)
	finish(79, 0, 1, "degraded", now, false)
	finish(5, 0, 1, "degraded", now, false)
	finish(5, 0, 1, "degraded", now, true)

	finish(5, 0, 3, "degraded", now.Add(-time.Minute), false)
	finish(79, 0, 3, "degraded", now.Add(-time.Minute), false)
	cutoff = now
	finish(5, 0, 3, "degraded", now.Add(-time.Second), false)
	finish(79, 0, 3, "degraded", now, false)
	finish(5, 0, 3, "degraded", now, false)
	finish(79, 0, 3, "degraded", now, true)

	// Re-read the configured threshold and serialize concurrent finishes for the same account.
	threshold = 2
	first, second := claim(5, 0), claim(79, 0)
	start := make(chan struct{})
	results := make(chan error, 2)
	before := pauses.Load()
	for _, c := range []*Claim{first, second} {
		go func(c *Claim) {
			<-start
			results <- store.Finish(ctx, c, Record{GroupID: c.Config.GroupID, AccountID: 4, Status: "degraded", CheckedAt: now})
		}(c)
	}
	close(start)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Equal(t, before+1, pauses.Load())
	var total, paused int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*),COUNT(*) FILTER (WHERE paused) FROM intelligence_check_runs WHERE account_id=4`).Scan(&total, &paused))
	require.Equal(t, 2, total)
	require.Equal(t, 1, paused)

	threshold = DefaultPauseThreshold
	finish(5, 0, 5, "degraded", now, true)
}
