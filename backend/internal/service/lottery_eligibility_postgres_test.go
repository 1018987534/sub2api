package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Eligibility changes delete entries, so exercise their transaction against a
// disposable PostgreSQL database rather than mocking the SQL statements.
func newLotteryEligibilityFixture(t *testing.T) (*LotteryService, LotteryConfig, context.Context) {
	t.Helper()
	dsn := os.Getenv("LOTTERY_TEST_DSN")
	if dsn == "" {
		t.Skip("set LOTTERY_TEST_DSN to a disposable PostgreSQL")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("lottery_eligibility_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn+" search_path="+schema)
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		_ = admin.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT,updated_at TIMESTAMPTZ DEFAULT NOW());
		CREATE TABLE users (id BIGINT PRIMARY KEY,email TEXT,username TEXT,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ,status TEXT DEFAULT 'active',balance NUMERIC DEFAULT 10);
		CREATE TABLE redeem_codes (used_by BIGINT,type TEXT,value NUMERIC,used_at TIMESTAMPTZ);
		INSERT INTO users (id,email) SELECT n,'eligibility-'||n||'@example.test' FROM generate_series(1,9) n;`)
	require.NoError(t, err)
	for _, name := range []string{"233_lottery.sql", "234_lottery_manual_progress.sql", "244_lottery_paused_rounds.sql"} {
		content, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(content))
		require.NoError(t, err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	_, err = db.ExecContext(ctx, `
		UPDATE users SET created_at=CASE WHEN id=4 THEN $2::timestamptz WHEN id=7 THEN $3::timestamptz ELSE $1::timestamptz END`, now.AddDate(0, 0, -30), now.AddDate(0, 0, -1), now.AddDate(0, 0, -7))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO redeem_codes VALUES
		(1,'admin_balance',10,$1),(3,'balance',4,$1),(4,'balance',10,$1),
		(5,'balance',10,$2),(6,'subscription',100,$1),(6,'balance',-10,$1),
		(6,'balance',100,NULL),(7,'balance',5,$3),(8,'balance',10,$1),(9,'balance',10,$1)`,
		now.AddDate(0, 0, -1), now.AddDate(0, 0, -10), now.AddDate(0, 0, -7))
	require.NoError(t, err)
	svc := NewLotteryService(db, nil, nil)
	svc.now = func() time.Time { return now }
	cfg := LotteryConfig{Enabled: true, ParticipantThreshold: 50, PrizeCount: 1, PrizeAmount: 5, DrawMode: "manual", NextRoundMode: "manual"}
	_, err = svc.UpdateConfig(ctx, cfg)
	require.NoError(t, err)
	return svc, cfg, ctx
}

func joinLotteryEligibilityFixture(t *testing.T, svc *LotteryService, ctx context.Context, ids ...int64) LotteryRound {
	t.Helper()
	round, err := svc.StartRound(ctx, 1)
	require.NoError(t, err)
	for _, id := range ids {
		_, err := svc.Join(ctx, id, "127.0.0.1")
		require.NoError(t, err)
	}
	return round
}

func TestLotteryEligibilitySaveRevalidatesActiveRounds(t *testing.T) {
	for _, status := range []string{LotteryRoundStatusOpen, LotteryRoundStatusPaused} {
		for _, tc := range []struct {
			name string
			edit func(*LotteryConfig)
			kept []int64
		}{
			{"recharge_required", func(c *LotteryConfig) { c.RequireRecharge = true }, []int64{1, 3, 4, 5, 7}},
			{"recharge_amount", func(c *LotteryConfig) { c.MinRechargeAmount = 5 }, []int64{1, 4, 5, 7}},
			{"account_age", func(c *LotteryConfig) { c.MinAccountAgeDays = 7 }, []int64{1, 2, 3, 5, 6, 7}},
			{"recent_recharge", func(c *LotteryConfig) { c.RecentRechargeDays = 7 }, []int64{1, 3, 4, 7}},
			{"combined", func(c *LotteryConfig) {
				c.RequireRecharge, c.MinRechargeAmount, c.MinAccountAgeDays, c.RecentRechargeDays = true, 5, 7, 7
			}, []int64{1, 7}},
		} {
			t.Run(status+"/"+tc.name, func(t *testing.T) {
				svc, cfg, ctx := newLotteryEligibilityFixture(t)
				round := joinLotteryEligibilityFixture(t, svc, ctx, 1, 2, 3, 4, 5, 6, 7)
				_, err := svc.UpdateProgress(ctx, round.ID, 12)
				require.NoError(t, err)
				if status == LotteryRoundStatusPaused {
					_, err = svc.UpdateRoundStatus(ctx, round.ID, status)
					require.NoError(t, err)
				}
				tc.edit(&cfg)
				cfg.ParticipantThreshold, cfg.PrizeCount, cfg.PrizeAmount = 60, 2, 9
				cfg.DrawMode, cfg.NextRoundMode = "auto", "auto"
				_, err = svc.UpdateConfig(ctx, cfg)
				require.NoError(t, err)
				page, err := svc.ListRounds(ctx, 1, 10)
				require.NoError(t, err)
				got := page.Items[0]
				require.Equal(t, status, got.Status)
				require.Equal(t, cfg.RequireRecharge, got.RequireRecharge)
				require.Equal(t, cfg.MinRechargeAmount, got.MinRechargeAmount)
				require.Equal(t, cfg.MinAccountAgeDays, got.MinAccountAgeDays)
				require.Equal(t, cfg.RecentRechargeDays, got.RecentRechargeDays)
				require.Equal(t, len(tc.kept), got.RealParticipantCount)
				require.Equal(t, 5, got.ManualProgressCount)
				require.Equal(t, len(tc.kept)+5, got.ParticipantCount)
				require.Equal(t, round.ParticipantThreshold, got.ParticipantThreshold)
				require.Equal(t, round.PrizeCount, got.PrizeCount)
				require.Equal(t, round.PrizeAmount, got.PrizeAmount)
				require.Equal(t, round.DrawMode, got.DrawMode)
				require.Equal(t, round.NextRoundMode, got.NextRoundMode)
				participants, err := svc.ListParticipants(ctx, round.ID, 1, 50)
				require.NoError(t, err)
				ids := make([]int64, 0, len(participants.Items))
				for _, participant := range participants.Items {
					ids = append(ids, participant.UserID)
				}
				require.ElementsMatch(t, tc.kept, ids)
				for id := int64(1); id <= 7; id++ {
					current, err := svc.GetCurrent(ctx, id)
					require.NoError(t, err)
					kept := false
					for _, validID := range tc.kept {
						kept = kept || validID == id
					}
					require.Equal(t, kept, current.Joined)
					if status == LotteryRoundStatusOpen {
						require.Equal(t, kept, current.Eligibility.Eligible)
					}
				}
				t.Logf("save -> %s round revalidated; real=7->%d manual=5 progress=12->%d", status, len(tc.kept), got.ParticipantCount)
			})
		}
	}
}

func TestLotteryEligibilityRelaxAllowsRejoinAndDrawExcludesRemoved(t *testing.T) {
	svc, cfg, ctx := newLotteryEligibilityFixture(t)
	round := joinLotteryEligibilityFixture(t, svc, ctx, 1, 2, 3)
	cfg.RequireRecharge, cfg.MinRechargeAmount = true, 5
	_, err := svc.UpdateConfig(ctx, cfg)
	require.NoError(t, err)
	_, err = svc.Join(ctx, 2, "127.0.0.1")
	require.ErrorIs(t, err, ErrLotteryNotEligible)
	cfg.RequireRecharge, cfg.MinRechargeAmount = false, 0
	_, err = svc.UpdateConfig(ctx, cfg)
	require.NoError(t, err)
	before, err := svc.ListParticipants(ctx, round.ID, 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), before.Total, "relaxing rules must not automatically re-enroll removed users")
	_, err = svc.Join(ctx, 2, "127.0.0.1")
	require.NoError(t, err, "removed users can rejoin the same round after rules are relaxed")
	cfg.RequireRecharge, cfg.MinRechargeAmount = true, 5
	_, err = svc.UpdateConfig(ctx, cfg)
	require.NoError(t, err)
	result, err := svc.DrawRound(ctx, round.ID)
	require.NoError(t, err)
	require.Len(t, result.Winners, 1)
	require.Equal(t, MaskLotteryEmail("eligibility-1@example.test"), result.Winners[0].Email)
	require.Equal(t, 1, result.Round.RealParticipantCount)
	var paidOut float64
	require.NoError(t, svc.db.QueryRowContext(ctx, `SELECT SUM(amount) FROM lottery_balance_ledger`).Scan(&paidOut))
	require.Equal(t, 5.0, paidOut)
	t.Log("tighten -> unpaid join denied; removed entries cannot win; only the eligible user receives $5")

	second, err := svc.StartRound(ctx, 1)
	require.NoError(t, err)
	_, err = svc.Join(ctx, 2, "127.0.0.1")
	require.ErrorIs(t, err, ErrLotteryNotEligible)
	cfg.RequireRecharge, cfg.MinRechargeAmount = false, 0
	_, err = svc.UpdateConfig(ctx, cfg)
	require.NoError(t, err)
	before, err = svc.ListParticipants(ctx, second.ID, 1, 10)
	require.NoError(t, err)
	require.Zero(t, before.Total)
	_, err = svc.Join(ctx, 2, "127.0.0.1")
	require.NoError(t, err)
	t.Log("relax -> no automatic enrollment; previously excluded user can explicitly join")
}

func TestLotteryEligibilitySavePreservesManualOffsetAndActors(t *testing.T) {
	svc, cfg, ctx := newLotteryEligibilityFixture(t)
	round := joinLotteryEligibilityFixture(t, svc, ctx, 1, 2, 3, 8, 9)
	_, err := svc.UpdateProgress(ctx, round.ID, 10)
	require.NoError(t, err)
	_, err = svc.db.ExecContext(ctx, `INSERT INTO lottery_participants(round_id,is_actor) VALUES($1,TRUE)`, round.ID)
	require.NoError(t, err)
	_, err = svc.db.ExecContext(ctx, `UPDATE users SET status='disabled' WHERE id=8; UPDATE users SET deleted_at=NOW() WHERE id=9`)
	require.NoError(t, err)
	cfg.RequireRecharge, cfg.MinRechargeAmount = true, 5
	_, err = svc.UpdateConfig(ctx, cfg)
	require.NoError(t, err)
	page, err := svc.ListRounds(ctx, 1, 10)
	require.NoError(t, err)
	require.Equal(t, 1, page.Items[0].RealParticipantCount)
	require.Equal(t, 5, page.Items[0].ManualProgressCount)
	require.Equal(t, 6, page.Items[0].ParticipantCount)
	var actors int
	require.NoError(t, svc.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lottery_participants WHERE is_actor=TRUE`).Scan(&actors))
	require.Equal(t, 1, actors)
}

func TestLotteryEligibilitySaveRollsBackConfigAndRemovalsOnFailure(t *testing.T) {
	svc, cfg, ctx := newLotteryEligibilityFixture(t)
	joinLotteryEligibilityFixture(t, svc, ctx, 1, 2, 3)
	_, err := svc.db.ExecContext(ctx, `
		CREATE FUNCTION reject_eligibility_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture rejects round update'; END $$;
		CREATE TRIGGER reject_eligibility_update BEFORE UPDATE ON lottery_rounds FOR EACH ROW EXECUTE FUNCTION reject_eligibility_update();`)
	require.NoError(t, err)
	cfg.Enabled, cfg.RequireRecharge, cfg.MinRechargeAmount = false, true, 5
	_, err = svc.UpdateConfig(ctx, cfg)
	require.ErrorContains(t, err, "fixture rejects round update")
	stored, err := svc.GetConfig(ctx)
	require.NoError(t, err)
	require.True(t, stored.Enabled)
	require.False(t, stored.RequireRecharge)
	require.Zero(t, stored.MinRechargeAmount)
	page, err := svc.ListRounds(ctx, 1, 10)
	require.NoError(t, err)
	require.Equal(t, 3, page.Items[0].RealParticipantCount)
	require.Equal(t, 3, page.Items[0].ParticipantCount)
	t.Log("round update fails after deletion -> settings, config, participant deletions and progress all rolled back")
}

func TestLotteryEligibilitySavePreservesFinishedRoundHistory(t *testing.T) {
	for _, status := range []string{LotteryRoundStatusDrawn, LotteryRoundStatusCancelled} {
		t.Run(status, func(t *testing.T) {
			svc, cfg, ctx := newLotteryEligibilityFixture(t)
			round := joinLotteryEligibilityFixture(t, svc, ctx, 1, 2, 3)
			if status == LotteryRoundStatusDrawn {
				_, err := svc.DrawRound(ctx, round.ID)
				require.NoError(t, err)
			} else {
				_, err := svc.UpdateRoundStatus(ctx, round.ID, status)
				require.NoError(t, err)
			}
			before, err := svc.ListRounds(ctx, 1, 10)
			require.NoError(t, err)
			cfg.RequireRecharge, cfg.MinRechargeAmount = true, 5
			_, err = svc.UpdateConfig(ctx, cfg)
			require.NoError(t, err)
			after, err := svc.ListRounds(ctx, 1, 10)
			require.NoError(t, err)
			require.Equal(t, before.Items, after.Items)
			next, err := svc.StartRound(ctx, 1)
			require.NoError(t, err)
			require.True(t, next.RequireRecharge)
			require.Equal(t, 5.0, next.MinRechargeAmount)
		})
	}
}

func TestLotteryEligibilitySaveSerializesConcurrentJoin(t *testing.T) {
	svc, cfg, ctx := newLotteryEligibilityFixture(t)
	round := joinLotteryEligibilityFixture(t, svc, ctx, 1)
	cfg.RequireRecharge = true
	var wg sync.WaitGroup
	var joinErr, saveErr error
	wg.Add(2)
	go func() { defer wg.Done(); _, joinErr = svc.Join(ctx, 2, "127.0.0.1") }()
	go func() { defer wg.Done(); _, saveErr = svc.UpdateConfig(ctx, cfg) }()
	wg.Wait()
	require.NoError(t, saveErr)
	if joinErr != nil {
		require.ErrorIs(t, joinErr, ErrLotteryNotEligible)
	}
	joined, err := svc.hasJoined(ctx, round.ID, 2)
	require.NoError(t, err)
	require.False(t, joined)
	page, err := svc.ListRounds(ctx, 1, 10)
	require.NoError(t, err)
	require.Equal(t, 1, page.Items[0].ParticipantCount)
	t.Log("concurrent join/save -> an ineligible user cannot remain enrolled after save commits")
}
