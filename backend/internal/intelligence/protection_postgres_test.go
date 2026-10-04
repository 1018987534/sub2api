package intelligence

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntelligenceProtectionPostgres(t *testing.T) {
	dsn := os.Getenv("INTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE intelligence_check_configs(group_id BIGINT PRIMARY KEY,lease_token TEXT,lease_until TIMESTAMPTZ);
 CREATE TEMP TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,group_id BIGINT,checked_at TIMESTAMPTZ,duration_ms BIGINT,status TEXT,answer TEXT,error TEXT,config_snapshot JSONB,account_id BIGINT,request_id TEXT,paused BOOLEAN,protection_eligible BOOLEAN);
 CREATE TEMP TABLE usage_logs(id BIGSERIAL PRIMARY KEY,request_id TEXT,api_key_id BIGINT,group_id BIGINT,account_id BIGINT);
 SET search_path=pg_temp;
 INSERT INTO intelligence_check_configs VALUES(7,NULL,NULL)`)
	require.NoError(t, err)
	pauses := 0
	s := &SQLStore{DB: db, ProtectionEligible: func(context.Context, Config) (bool, error) { return true, nil }, Protect: func(_ context.Context, c Config, id int64) (bool, error) {
		require.Equal(t, int64(7), c.GroupID)
		require.Equal(t, int64(1), id)
		pauses++
		return true, nil
	}}
	c := DefaultConfig(7)
	c.APIKeyID = 9
	tests := []struct {
		account int64
		status  string
		paused  bool
	}{
		{1, "degraded", false}, {2, "normal", false}, {1, "degraded", true},
		{1, "degraded", true}, {1, "normal", false}, {1, "degraded", false},
		{1, "error", false}, {1, "degraded", false}, {0, "error", false},
		{1, "degraded", true}, {1, "degraded", true},
	}
	for i, tc := range tests {
		rid := ""
		if tc.account > 0 {
			rid = fmt.Sprintf("client:fixture-%d", i)
			_, err = db.ExecContext(ctx, `INSERT INTO usage_logs(request_id,api_key_id,group_id,account_id) VALUES($1,9,7,$2)`, rid, tc.account)
			require.NoError(t, err)
			// Same request ID on another API key must never win attribution.
			_, err = db.ExecContext(ctx, `INSERT INTO usage_logs(request_id,api_key_id,group_id,account_id) VALUES($1,99,7,999)`, rid)
			require.NoError(t, err)
		}
		_, err = db.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_token='owned' WHERE group_id=7`)
		require.NoError(t, err)
		err = s.Finish(ctx, &Claim{Config: c, Token: "owned"}, Record{GroupID: 7, Status: tc.status, RequestID: rid, CheckedAt: time.Now()})
		require.NoError(t, err)
		var gotID int64
		var paused bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(account_id,0),paused FROM intelligence_check_runs ORDER BY id DESC LIMIT 1`).Scan(&gotID, &paused))
		require.Equal(t, tc.account, gotID)
		require.Equal(t, tc.paused, paused)
		t.Logf("check=%d account=%d status=%s paused=%v", i+1, gotID, tc.status, paused)
	}
	require.Equal(t, 4, pauses)
	_, err = db.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_token='new-owner'`)
	require.NoError(t, err)
	err = s.Finish(ctx, &Claim{Config: c, Token: "stale"}, Record{GroupID: 7, Status: "degraded", AccountID: 1, CheckedAt: time.Now()})
	require.ErrorIs(t, err, ErrConflict)
	require.Equal(t, 4, pauses)
	history, err := s.History(ctx, 7, time.Now().Add(-time.Hour), 0, 100)
	require.NoError(t, err)
	require.Len(t, history, len(tests))
	require.Equal(t, int64(1), history[0].AccountID)
	require.True(t, history[0].Paused)
}

func TestIntelligenceGlobalAccountPostgres(t *testing.T) {
	dsn := os.Getenv("INTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE intelligence_check_configs(group_id BIGINT PRIMARY KEY,lease_token TEXT,lease_until TIMESTAMPTZ);
 CREATE TEMP TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,group_id BIGINT,checked_at TIMESTAMPTZ,duration_ms BIGINT,status TEXT,answer TEXT,error TEXT,config_snapshot JSONB,account_id BIGINT,request_id TEXT,paused BOOLEAN,protection_eligible BOOLEAN);
 SET search_path=pg_temp;INSERT INTO intelligence_check_configs VALUES(7,NULL,NULL),(8,NULL,NULL),(9,NULL,NULL);`)
	require.NoError(t, err)
	pauses, recoveries := 0, 0
	s := &SQLStore{DB: db, ProtectionEligible: func(_ context.Context, c Config) (bool, error) { return c.GroupID != 9, nil }, Protect: func(context.Context, Config, int64) (bool, error) { pauses++; return true, nil }, Recover: func(context.Context, int64) error { recoveries++; return nil }}
	steps := []struct {
		group, account     int64
		status             string
		pauses, recoveries int
	}{
		{7, 1, "degraded", 0, 0}, {9, 1, "normal", 0, 0}, {8, 1, "degraded", 1, 0},
		{7, 1, "normal", 1, 1}, {8, 1, "degraded", 1, 1}, {7, 2, "degraded", 1, 1}, {7, 1, "degraded", 2, 1},
		{9, 1, "normal", 2, 1}, {8, 1, "normal", 2, 2}, {9, 1, "degraded", 2, 2}, {7, 1, "degraded", 2, 2},
		{8, 1, "error", 2, 2}, {7, 1, "degraded", 2, 2}, {8, 0, "error", 2, 2}, {8, 1, "degraded", 3, 2},
	}
	for i, step := range steps {
		_, err = db.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_token='owned' WHERE group_id=$1`, step.group)
		require.NoError(t, err)
		err = s.Finish(ctx, &Claim{Config: DefaultConfig(step.group), Token: "owned"}, Record{GroupID: step.group, AccountID: step.account, Status: step.status, CheckedAt: time.Now()})
		require.NoError(t, err)
		require.Equal(t, step.pauses, pauses)
		require.Equal(t, step.recoveries, recoveries)
		t.Logf("step=%d group=%d account=%d status=%s total_pauses=%d recoveries=%d", i+1, step.group, step.account, step.status, pauses, recoveries)
	}
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts(id BIGINT,temp_unschedulable_until TIMESTAMPTZ,temp_unschedulable_reason TEXT);
 INSERT INTO accounts VALUES(1,NOW()+INTERVAL '20 minutes','intelligence: legacy'),(2,NOW()+INTERVAL '20 minutes','unrelated');`)
	require.NoError(t, err)
	before := pauses
	require.NoError(t, s.ReconcileLegacyProtection(ctx))
	require.Equal(t, before+1, pauses)
	t.Log("legacy reconciliation: active intelligence pause reconsidered; unrelated pause untouched")

}
