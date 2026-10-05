package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceGroupChangeReleasesSamples(t *testing.T) {
	_, rdb, latency := newTotalLatencyTestCache(t)
	ctx := context.Background()
	cache := NewSchedulerCache(rdb)
	account := &service.Account{ID: 7, Extra: map[string]any{
		"intelligence_pause_until": nil, "intelligence_allowed_groups": nil,
		"intelligence_protected_groups": nil, "intelligence_recovery_required": false,
	}}
	require.NoError(t, latency.ResetForIntelligencePause(ctx, 7, time.Now().Add(20*time.Minute)))
	require.NoError(t, cache.SetAccount(ctx, account))
	require.NoError(t, latency.RecordSample(ctx, 7, "after-membership-change", 1000))
	stats, err := latency.GetStatsBatch(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats[7].SampleCount)
	require.False(t, stats[7].ReliableFast)
	projected, err := cache.GetAccount(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, false, projected.Extra["intelligence_recovery_required"])
}

func TestIntelligenceGroupChangePostgres(t *testing.T) {
	dsn := os.Getenv("INTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL session required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts(id BIGINT PRIMARY KEY,extra JSONB,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
	CREATE TEMP TABLE groups(id BIGINT PRIMARY KEY,deleted_at TIMESTAMPTZ);
	CREATE TEMP TABLE account_groups(account_id BIGINT,group_id BIGINT,priority INTEGER,created_at TIMESTAMPTZ,PRIMARY KEY(account_id,group_id));
	CREATE TEMP TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT UNIQUE,created_at TIMESTAMPTZ DEFAULT NOW());
	SET search_path=pg_temp;
	INSERT INTO accounts VALUES(7,'{}',NOW(),NULL);
	INSERT INTO groups VALUES(5,NULL),(79,NULL),(103,NULL),(111,NULL);
	INSERT INTO account_groups VALUES(7,103,1,NOW())`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	groupRepo := newGroupRepositoryWithSQL(client, db)
	seed := func() {
		_, err := db.ExecContext(ctx, `UPDATE accounts SET extra='{"intelligence_pause_until":"2030-01-01T00:00:00Z","intelligence_allowed_groups":[103],"intelligence_protected_groups":[5,79,111],"intelligence_recovery_required":true,"unrelated":"keep"}'`)
		require.NoError(t, err)
	}
	check := func(name string, wantRecovery bool) {
		var raw []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id=7`).Scan(&raw))
		var extra map[string]any
		require.NoError(t, json.Unmarshal(raw, &extra))
		t.Logf("%s recovery_required=%v pause_until=%v allowed_groups=%v protected_groups=%v", name, extra["intelligence_recovery_required"], extra["intelligence_pause_until"], extra["intelligence_allowed_groups"], extra["intelligence_protected_groups"])
		require.Equal(t, wantRecovery, extra["intelligence_recovery_required"])
		require.Equal(t, "keep", extra["unrelated"])
		if !wantRecovery {
			require.Nil(t, extra["intelligence_pause_until"])
			require.Nil(t, extra["intelligence_allowed_groups"])
			require.Nil(t, extra["intelligence_protected_groups"])
		}
	}
	seed()
	require.NoError(t, repo.AddToGroup(ctx, 7, 111, 2))
	check("add", false)
	seed()
	require.NoError(t, repo.RemoveFromGroup(ctx, 7, 103))
	check("remove", false)
	seed()
	require.NoError(t, repo.RemoveFromGroup(ctx, 7, 103))
	check("remove-no-change", true)
	seed()
	require.NoError(t, repo.BindGroups(ctx, 7, []int64{5, 79}))
	check("replace", false)
	seed()
	require.NoError(t, repo.BindGroups(ctx, 7, []int64{5, 79}))
	check("same-bindings", true)
	seed()
	require.NoError(t, repo.BindGroups(ctx, 7, []int64{79, 5}))
	check("priority-change", false)
	seed()
	require.NoError(t, repo.BindGroups(ctx, 7, nil))
	check("clear", false)
	require.NoError(t, repo.AddToGroup(ctx, 7, 111, 1))
	seed()
	affected, err := groupRepo.DeleteAccountGroupsByGroupID(ctx, 111)
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)
	check("delete-group-bindings", false)
}
