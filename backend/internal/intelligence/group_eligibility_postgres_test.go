package intelligence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Tables are session-local and search_path excludes production tables. No
// HTTP probe is created; this exercises the real claim and reconciliation SQL.
func TestIntelligenceGroupEligibilityPostgres(t *testing.T) {
	dsn := os.Getenv("INTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("INTELLIGENCE_TEST_POSTGRES_DSN is not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE groups(id BIGINT PRIMARY KEY,name TEXT,status TEXT,deleted_at TIMESTAMPTZ);
 CREATE TEMP TABLE intelligence_check_configs(group_id BIGINT PRIMARY KEY,version INTEGER NOT NULL DEFAULT 1,config JSONB NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,interval_minutes INTEGER NOT NULL DEFAULT 10,timeout_seconds INTEGER NOT NULL DEFAULT 900,
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),lease_token TEXT,lease_until TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
 CREATE TEMP TABLE intelligence_check_model_configs (LIKE intelligence_check_configs INCLUDING DEFAULTS, id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY);
 SET search_path=pg_temp;
 INSERT INTO groups VALUES(1,'不降智-稳定','active',NULL),(2,'013不降智','inactive',NULL),(3,'DeepSeek','active',NULL),(4,'不降智-已删除','active',NOW())`)
	require.NoError(t, err)
	for id := int64(1); id <= 4; id++ {
		c := DefaultConfig(id)
		c.Enabled, c.APIKeyID = true, 42
		raw, err := json.Marshal(c)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `INSERT INTO intelligence_check_configs(group_id,config)VALUES($1,$2)`, id, raw)
		require.NoError(t, err)
	}
	s := SQLStore{DB: db}
	configs, err := s.Configs(ctx)
	require.NoError(t, err)
	visible, enabled := []int64{}, []int64{}
	for _, c := range configs {
		visible = append(visible, c.GroupID)
	}
	rows, err := db.QueryContext(ctx, `SELECT group_id FROM intelligence_check_configs WHERE enabled OR config->'enabled'='true'::jsonb ORDER BY group_id`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		enabled = append(enabled, id)
	}
	require.NoError(t, rows.Close())
	manual := map[int64]bool{}
	for _, id := range []int64{2, 3, 4} {
		_, err = s.Claim(ctx, ProbeKey{GroupID: id}, true)
		require.True(t, err == nil || err == ErrConflict)
		manual[id] = err == nil
	}
	_, err = db.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_until=NULL,lease_token=NULL,next_run_at=NOW()`)
	require.NoError(t, err)
	automatic := []int64{}
	for i := 0; i < 5; i++ {
		claim, err := s.Claim(ctx, ProbeKey{}, false)
		if err == ErrConflict {
			break
		}
		require.NoError(t, err)
		automatic = append(automatic, claim.Config.GroupID)
	}
	observed, err := json.Marshal(map[string]any{"visible": visible, "enabled": enabled, "manual_allowed": manual, "automatic_claimed": automatic})
	require.NoError(t, err)
	fmt.Printf("ELIGIBILITY_OBSERVED %s\n", observed)
	require.Equal(t, []int64{1}, visible)
	require.Equal(t, []int64{1}, enabled)
	require.Equal(t, map[int64]bool{2: false, 3: false, 4: false}, manual)
	require.Equal(t, []int64{1}, automatic)
	// Repeated reads preserve CAS versions. A rename back never resumes probes.
	_, err = s.Configs(ctx)
	require.NoError(t, err)
	var version int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT version FROM intelligence_check_configs WHERE group_id=2`).Scan(&version))
	require.Equal(t, 2, version)
	_, err = db.ExecContext(ctx, `UPDATE groups SET name='普通分组' WHERE id=1`)
	require.NoError(t, err)
	configs, err = s.Configs(ctx)
	require.NoError(t, err)
	require.Empty(t, configs)
	_, err = db.ExecContext(ctx, `UPDATE groups SET name='不降智-稳定' WHERE id=1`)
	require.NoError(t, err)
	configs, err = s.Configs(ctx)
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.False(t, configs[0].Enabled)
	_, err = s.Claim(ctx, ProbeKey{}, false)
	require.ErrorIs(t, err, ErrConflict)
}
