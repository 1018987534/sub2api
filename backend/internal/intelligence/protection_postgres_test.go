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
 CREATE TEMP TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,group_id BIGINT,checked_at TIMESTAMPTZ,duration_ms BIGINT,status TEXT,answer TEXT,error TEXT,config_snapshot JSONB,account_id BIGINT,request_id TEXT,paused BOOLEAN);
 CREATE TEMP TABLE usage_logs(id BIGSERIAL PRIMARY KEY,request_id TEXT,api_key_id BIGINT,group_id BIGINT,account_id BIGINT);
 SET search_path=pg_temp;
 INSERT INTO intelligence_check_configs VALUES(7,NULL,NULL)`)
	require.NoError(t, err)
	pauses := 0
	s := &SQLStore{DB: db, Protect: func(_ context.Context, c Config, id int64) (bool, error) {
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
		{1, "degraded", false}, {1, "normal", false}, {1, "degraded", false},
		{1, "error", false}, {1, "degraded", false}, {0, "error", false},
		{1, "degraded", false}, {1, "degraded", true},
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
	require.Equal(t, 2, pauses)
	_, err = db.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_token='new-owner'`)
	require.NoError(t, err)
	err = s.Finish(ctx, &Claim{Config: c, Token: "stale"}, Record{GroupID: 7, Status: "degraded", AccountID: 1, CheckedAt: time.Now()})
	require.ErrorIs(t, err, ErrConflict)
	require.Equal(t, 2, pauses)
	history, err := s.History(ctx, 7, time.Now().Add(-time.Hour), 0, 100)
	require.NoError(t, err)
	require.Len(t, history, len(tests))
	require.Equal(t, int64(1), history[0].AccountID)
	require.True(t, history[0].Paused)
}
