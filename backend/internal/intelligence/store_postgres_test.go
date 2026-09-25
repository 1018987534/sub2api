package intelligence

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Run against a disposable PostgreSQL database with INTELLIGENCE_TEST_POSTGRES_DSN.
// A session-local table shadows the real table; no persistent data or paid probes are touched.
func TestIntelligenceSavePostgres(t *testing.T) {
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
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE intelligence_check_configs (
 group_id BIGINT PRIMARY KEY, version INTEGER NOT NULL DEFAULT 1, config JSONB NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT FALSE, interval_minutes INTEGER NOT NULL,
 timeout_seconds INTEGER NOT NULL, next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_token TEXT, lease_until TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
	require.NoError(t, err)
	// Exclude public entirely: even an accidental connection change cannot access a real table.
	_, err = db.ExecContext(ctx, `SET search_path = pg_temp`)
	require.NoError(t, err)
	s := SQLStore{DB: db}
	c, err := s.Save(ctx, DefaultConfig(99))
	require.NoError(t, err)
	require.Equal(t, 1, c.Version)
	t.Log("INSERT version=1 interval=10 timeout=900")
	_, err = db.ExecContext(ctx, `UPDATE intelligence_check_configs SET lease_token='in-flight', lease_until=NOW()+INTERVAL '15 minutes'`)
	require.NoError(t, err)
	stale := c
	c.IntervalMinutes = 5
	c.TimeoutSeconds = 900
	c.Prompt = "updated prompt"
	c.Expected = []string{"21", "手感"}
	c, err = s.Save(ctx, c)
	require.NoError(t, err)
	require.Equal(t, 2, c.Version)
	var interval, timeout int
	var token string
	var scheduledSeconds float64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT interval_minutes,timeout_seconds,lease_token,EXTRACT(EPOCH FROM (next_run_at-NOW())) FROM intelligence_check_configs`).Scan(&interval, &timeout, &token, &scheduledSeconds))
	require.Equal(t, 5, interval)
	require.Equal(t, 900, timeout)
	require.Equal(t, "in-flight", token)
	require.InDelta(t, 300, scheduledSeconds, 5)
	configs, err := s.Configs(ctx)
	require.NoError(t, err)
	require.Equal(t, []Config{c}, configs)
	t.Log("UPDATE version=2 interval=5 timeout=900 next_run=+5min lease=preserved readback=equal")
	_, err = s.Save(ctx, stale)
	require.ErrorIs(t, err, ErrConflict)
	t.Log("STALE version=1 rejected with ErrConflict")
	c.IntervalMinutes = 1440
	c, err = s.Save(ctx, c)
	require.NoError(t, err)
	require.Equal(t, 3, c.Version)
	t.Log("UPDATE version=3 interval=1440 accepted")
}
