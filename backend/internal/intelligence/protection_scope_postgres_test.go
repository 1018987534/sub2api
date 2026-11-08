package intelligence

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestIntelligenceProtectionScopeReconciliationPostgres(t *testing.T) {
	dsn := os.Getenv("INTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts(id BIGINT,temp_unschedulable_until TIMESTAMPTZ,temp_unschedulable_reason TEXT,extra JSONB,deleted_at TIMESTAMPTZ);
 CREATE TEMP TABLE groups(id BIGINT,name TEXT,platform TEXT,status TEXT,deleted_at TIMESTAMPTZ);
 CREATE TEMP TABLE account_groups(account_id BIGINT,group_id BIGINT);
 CREATE TEMP TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,account_id BIGINT,status TEXT,config_snapshot JSONB,protection_eligible BOOLEAN);
 SET search_path=pg_temp;
 INSERT INTO groups VALUES(79,'020不降智','openai','active',NULL),(107,'030企业满血不降智','openai','active',NULL),(103,'测试','openai','active',NULL),(108,'不降智停用','openai','disabled',NULL),(109,'不降智已删除','openai','active',NOW());
 INSERT INTO accounts(id,temp_unschedulable_until,temp_unschedulable_reason,extra) VALUES
 (1,NULL,'','{"intelligence_recovery_required":true,"intelligence_protected_groups":[79]}'),
 (2,NULL,'','{"intelligence_recovery_required":true,"intelligence_protected_groups":[79,107]}'),
 (3,NULL,'','{}'),
 (4,NOW()+INTERVAL '1 hour','unrelated','{}'),
 (5,NULL,'','{"intelligence_recovery_required":true,"intelligence_protected_groups":[79]}'),
 (6,NULL,'','{"intelligence_recovery_required":true,"intelligence_protected_groups":[79]}'),
 (7,NULL,'','{"intelligence_recovery_required":true,"intelligence_protected_groups":[79]}'),
 (8,NULL,'','{"intelligence_recovery_required":true,"intelligence_protected_groups":[79]}'),
 (9,NULL,'','{"intelligence_recovery_required":true,"intelligence_protected_groups":[79]}');
 UPDATE accounts SET deleted_at=NOW() WHERE id=9;
 INSERT INTO account_groups VALUES(1,79),(1,107),(2,79),(2,107),(3,107),(4,107),(5,79),(5,103),(6,79),(6,108),(7,79),(7,107),(8,79),(8,109),(9,107);`)
	require.NoError(t, err)
	config := DefaultConfig(79)
	config.Model = "gpt-6.1-sol"
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	for id := 1; id <= 9; id++ {
		status := "degraded"
		if id == 7 {
			status = "normal"
		}
		_, err = db.ExecContext(ctx, `INSERT INTO intelligence_check_runs(account_id,status,config_snapshot,protection_eligible)VALUES($1,$2,$3,true)`, id, status, string(raw))
		require.NoError(t, err)
	}
	var paused, recovered []int64
	store := &SQLStore{DB: db, Protect: func(_ context.Context, c Config, id int64) (bool, error) {
		require.Equal(t, config.Model, c.Model)
		paused = append(paused, id)
		return true, nil
	}, Recover: func(_ context.Context, id int64) error { recovered = append(recovered, id); return nil }}
	require.NoError(t, store.ReconcileProtectionScopes(ctx))
	require.Equal(t, []int64{1}, paused)
	require.Equal(t, []int64{7}, recovered)
	t.Log("only newly protected, isolated accounts are reconsidered; ordinary, disabled, deleted, healthy and unrelated cooldown accounts stay untouched")
	_, err = db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(extra,'{intelligence_protected_groups}','[79,107]') WHERE id=1;UPDATE accounts SET extra='{}' WHERE id=7`)
	require.NoError(t, err)
	require.NoError(t, store.ReconcileProtectionScopes(ctx))
	require.Len(t, paused, 1)
	require.Len(t, recovered, 1)
	t.Log("unchanged protection scopes are a no-op on following ticks")
}
