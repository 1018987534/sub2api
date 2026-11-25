package intelligence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceMultiModelPostgres(t *testing.T) {
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
	schema := "intelligence_models_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.ExecContext(ctx, `CREATE SCHEMA `+schema+`; SET search_path TO `+schema+`;`)
	require.NoError(t, err)
	defer func() {
		if _, err := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	}()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	migrate := func(name string) {
		t.Helper()
		data, err := os.ReadFile("../../migrations/" + name)
		require.NoError(t, err)
		exec(string(data))
	}
	exec(`CREATE TABLE groups (id BIGINT PRIMARY KEY,name TEXT,status TEXT,deleted_at TIMESTAMPTZ,platform TEXT);
 CREATE TABLE accounts (id BIGINT PRIMARY KEY);
 INSERT INTO groups VALUES(1,'不降智-多模型','active',NULL,'openai'),(2,'不降智-其他组','active',NULL,'openai')`)
	migrate("241_intelligence_checks.sql")
	migrate("242_intelligence_account_protection.sql")
	migrate("243_intelligence_account_scope.sql")
	primary := DefaultConfig(1)
	primary.Model, primary.Enabled, primary.APIKeyID = "original-model", true, 42
	raw, err := json.Marshal(primary)
	require.NoError(t, err)
	exec(`INSERT INTO intelligence_check_configs(group_id,config,enabled,interval_minutes,timeout_seconds)
 VALUES (1,$1,TRUE,10,900)`, raw)
	legacyInsert := `INSERT INTO intelligence_check_runs(group_id,checked_at,duration_ms,status,answer,config_snapshot)
 VALUES (1,NOW(),123,'normal','21',$1)`
	exec(legacyInsert, raw)
	migrate("256_intelligence_additional_model_checks.sql")
	exec(legacyInsert, raw) // Prior binaries still write primary records after the migration.

	s := SQLStore{DB: db, ProtectionEligible: func(_ context.Context, c Config) (bool, error) {
		require.Zero(t, c.ProbeID, "secondary checks must never reach account protection")
		return true, nil
	}}
	configs, err := s.Configs(ctx)
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.Equal(t, primary.Model, configs[0].Model)
	primary = configs[0]
	extra := DefaultConfig(1)
	extra.Model, extra.Enabled, extra.APIKeyID, extra.IntervalMinutes = " second-model ", true, 42, 5
	second, err := s.AddModel(ctx, extra)
	require.NoError(t, err)
	require.Positive(t, second.ProbeID)
	require.Equal(t, "second-model", second.Model)
	_, err = s.AddModel(ctx, extra)
	require.ErrorIs(t, err, ErrDuplicateModel)
	primaryEdit := primary
	primaryEdit.Model = second.Model
	_, err = s.Save(ctx, primaryEdit)
	require.ErrorIs(t, err, ErrDuplicateModel)
	_, err = s.Claim(ctx, ProbeKey{GroupID: 2, ProbeID: second.ProbeID}, true)
	require.ErrorIs(t, err, ErrConflict)

	exec(`UPDATE intelligence_check_model_configs SET next_run_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, second.ProbeID)
	modelClaim, err := s.Claim(ctx, ProbeKey{}, false)
	require.NoError(t, err)
	require.Equal(t, second.Key(), modelClaim.Config.Key())
	primaryClaim, err := s.Claim(ctx, ProbeKey{}, false)
	require.NoError(t, err)
	require.Equal(t, primary.Key(), primaryClaim.Config.Key())
	_, err = s.Claim(ctx, second.Key(), true)
	require.ErrorIs(t, err, ErrConflict)
	require.ErrorIs(t, s.DeleteModel(ctx, second.Key(), second.Version), ErrConflict)
	second.Prompt = "updated prompt"
	saved, err := s.Save(ctx, second)
	require.NoError(t, err)
	_, err = s.Save(ctx, second)
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.Claim(ctx, saved.Key(), true)
	require.ErrorIs(t, err, ErrConflict)
	now := time.Now()
	run := Record{GroupID: 1, CheckedAt: now, Status: "degraded", Answer: "wrong"}
	require.NoError(t, s.Finish(ctx, modelClaim, run))
	require.NoError(t, s.Finish(ctx, primaryClaim, run))
	require.ErrorIs(t, s.Finish(ctx, modelClaim, run), ErrConflict)
	var secondaryEligible bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT protection_eligible FROM intelligence_check_runs WHERE probe_id=$1`, saved.ProbeID).Scan(&secondaryEligible))
	require.False(t, secondaryEligible)
	from := now.Add(-time.Hour)
	history, err := s.History(ctx, saved.Key(), from, 0, 25)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, modelClaim.Config.Prompt, history[0].Config.Prompt)
	detail, err := s.Record(ctx, 1, history[0].ID, from)
	require.NoError(t, err)
	require.Equal(t, saved.ProbeID, detail.Config.ProbeID)
	_, err = s.Record(ctx, 2, history[0].ID, from)
	require.ErrorIs(t, err, sql.ErrNoRows)
	timelines, err := s.Timelines(ctx, []Config{primary, saved}, from, 2)
	require.NoError(t, err)
	require.Len(t, timelines[primary.Key()], 2)
	require.Len(t, timelines[saved.Key()], 1)
	require.Equal(t, history[0].ID, timelines[saved.Key()][0].ID)
	modelRenamed := saved
	modelRenamed.Model = "new-model"
	modelRenamed, err = s.Save(ctx, modelRenamed)
	require.NoError(t, err)
	timelines, err = s.Timelines(ctx, []Config{modelRenamed}, from, 60)
	require.NoError(t, err)
	require.Empty(t, timelines)
	require.ErrorIs(t, s.DeleteModel(ctx, saved.Key(), saved.Version), ErrConflict)
	require.NoError(t, s.DeleteModel(ctx, modelRenamed.Key(), modelRenamed.Version))
	_, err = s.Record(ctx, 1, history[0].ID, from)
	require.NoError(t, err, "deleting a configuration preserves its run records until retention expires")

	for index := 1; index < MaxGroupModels; index++ {
		extra.Model = fmt.Sprintf("model-%d", index)
		_, err = s.AddModel(ctx, extra)
		require.NoError(t, err)
	}
	extra.Model = "over-limit"
	_, err = s.AddModel(ctx, extra)
	require.ErrorIs(t, err, ErrModelLimit)
	exec(`UPDATE groups SET name='普通分组' WHERE id=1`)
	configs, err = s.Configs(ctx)
	require.NoError(t, err)
	require.Empty(t, configs)
	exec(`UPDATE groups SET name='不降智-多模型' WHERE id=1`)
	configs, err = s.Configs(ctx)
	require.NoError(t, err)
	require.Len(t, configs, MaxGroupModels)
	for _, cfg := range configs {
		require.False(t, cfg.Enabled)
	}
}
