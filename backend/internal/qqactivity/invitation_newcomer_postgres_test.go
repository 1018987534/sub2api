package qqactivity

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func invitationSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func invitationMoney(t *testing.T, db *sql.DB, inviter, invitee int64, balance, quota float64) {
	t.Helper()
	var gotBalance, gotQuota float64
	if err := db.QueryRow("SELECT balance,COALESCE((SELECT aff_quota FROM user_affiliates WHERE user_id=$1),0) FROM users WHERE id=$2", inviter, invitee).Scan(&gotBalance, &gotQuota); err != nil {
		t.Fatal(err)
	}
	t.Logf("newcomer_balance=%.0f inviter_quota=%.0f", gotBalance, gotQuota)
	if gotBalance != balance || gotQuota != quota {
		t.Fatalf("balance/quota=%v/%v want=%v/%v", gotBalance, gotQuota, balance, quota)
	}
}

func invitationJoin(id, qq, inviter string) Event {
	level := 21
	return Event{ID: id, GroupID: "87654321", QQ: qq, Kind: "join", Time: time.Now().Unix(), Level: &level, MemberConfirmed: true, InviterQQ: inviter, EvidenceType: "direct_invite_pinned", EvidenceKey: "fixture:" + id}
}

func TestPostgresInvitationRequiresPaidNewcomer(t *testing.T) {
	ctx := context.Background()
	off, on := false, true
	setup := func(t *testing.T, existing bool) (*sql.DB, *Store, int64, int64) {
		t.Helper()
		db, s := autoBindingFixture(t)
		inviter := autoAdd(t, db, "12345678@qq.com", "")
		var invitee int64
		if existing {
			invitee = autoAdd(t, db, "23456789@qq.com", "")
		}
		autoInitialize(t, s, "12345678")
		invitationSQL(t, db, "UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'")
		invitationSQL(t, db, "UPDATE qq_activity_members SET qq_level=7 WHERE qq='12345678'")
		if !existing {
			invitee = autoAdd(t, db, "23456789@qq.com", "")
		}
		return db, s, inviter, invitee
	}
	process := func(t *testing.T, s *Store, e Event) {
		t.Helper()
		if err := s.Process(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	setNewcomer := func(t *testing.T, s *Store, enabled bool) {
		t.Helper()
		if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{NewcomerEnabled: &enabled}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("existing_invitee_and_manual_evidence_cannot_bypass_payment", func(t *testing.T) {
		db, s, inviter, invitee := setup(t, true)
		process(t, s, invitationJoin("old-join", "23456789", "12345678"))
		if err := s.VerifyInvitation(ctx, "fixture", "23456789", "12345678", "87654321", "fixture actual first invitation evidence"); err != nil {
			t.Fatal(err)
		}
		process(t, s, invitationJoin("old-rejoin", "23456789", "12345678"))
		invitationMoney(t, db, inviter, invitee, 0, 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards", 0)
	})
	t.Run("newcomer_disabled_then_automatic_ordered_payment_once", func(t *testing.T) {
		db, s, inviter, invitee := setup(t, false)
		setNewcomer(t, s, off)
		process(t, s, invitationJoin("new-join", "23456789", "12345678"))
		invitationMoney(t, db, inviter, invitee, 0, 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='pending'", 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_invitations WHERE status='awaiting_newcomer'", 1)
		summary, err := s.UserSummary(ctx, inviter)
		if err != nil || summary.InvitationPending != 0 || len(summary.Rewards) != 0 {
			t.Fatalf("unpaid newcomer advertised as confirmed invitation: %+v %v", summary, err)
		}
		report, err := s.AdminReport(ctx)
		if err != nil || report.PendingAmount != 2 {
			t.Fatalf("pending amount should count newcomer only: %+v %v", report, err)
		}
		// The inviter's user ID is lower. A trigger proves the newcomer has
		// already been paid when invitation ledger insertion is attempted.
		invitationSQL(t, db, `CREATE FUNCTION require_paid_newcomer() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF NOT EXISTS(SELECT 1 FROM qq_activity_rewards WHERE kind='newcomer' AND status='paid' AND subject_user_id=NEW.source_user_id)
			THEN RAISE EXCEPTION 'invitation preceded newcomer payment'; END IF; RETURN NEW; END $$;
			CREATE TRIGGER require_paid_newcomer BEFORE INSERT ON user_affiliate_ledger FOR EACH ROW EXECUTE FUNCTION require_paid_newcomer()`)
		setNewcomer(t, s, on)
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, `SELECT count(*) FROM qq_activity_outbox n,qq_activity_outbox i WHERE n.text LIKE '%体验额度已到账%' AND i.text LIKE '%邀请余额已到账%' AND n.id<i.id`, 1)
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs <- s.Process(ctx, invitationJoin(fmt.Sprintf("repeat-%d", i), "23456789", "34567890"))
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='paid'", 2)
		autoCount(t, db, "SELECT count(*) FROM user_affiliate_ledger", 1)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_invitations WHERE inviter_qq='12345678' AND status='paid'", 1)
	})
	t.Run("historical_pending_without_newcomer_stays_unpaid", func(t *testing.T) {
		db, s, inviter, invitee := setup(t, true)
		process(t, s, invitationJoin("old-join", "23456789", "12345678"))
		invitationSQL(t, db, `INSERT INTO qq_activity_rewards(kind,recipient_qq,recipient_user_id,subject_qq,subject_user_id) VALUES('invitation','12345678',$1,'23456789',$2)`, inviter, invitee)
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 0, 0)
		summary, err := s.UserSummary(ctx, inviter)
		if err != nil || summary.InvitationPending != 0 || len(summary.Rewards) != 0 {
			t.Fatalf("historical blocked invitation advertised: %+v %v", summary, err)
		}
		report, err := s.AdminReport(ctx)
		if err != nil || report.PendingAmount != 0 {
			t.Fatalf("blocked invitation counted as payable: %+v %v", report, err)
		}
		// Previously credited history remains visible and is not clawed back.
		invitationSQL(t, db, "UPDATE qq_activity_rewards SET status='paid',paid_at=NOW()")
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		summary, err = s.UserSummary(ctx, inviter)
		if err != nil || summary.InvitationPaid != 2 || len(summary.Rewards) != 1 {
			t.Fatalf("historical paid invitation lost: %+v %v", summary, err)
		}
	})
	for _, mismatch := range []string{"qq", "user"} {
		t.Run("paid_newcomer_must_match_"+mismatch, func(t *testing.T) {
			db, s, inviter, invitee := setup(t, false)
			setNewcomer(t, s, off)
			process(t, s, invitationJoin("new-join", "23456789", "12345678"))
			if mismatch == "qq" {
				invitationSQL(t, db, "UPDATE qq_activity_rewards SET status='paid',subject_qq='34567890',paid_at=NOW() WHERE kind='newcomer'")
			} else {
				invitationSQL(t, db, "UPDATE qq_activity_rewards SET status='paid',subject_user_id=9999,paid_at=NOW() WHERE kind='newcomer'")
			}
			if err := s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			invitationMoney(t, db, inviter, invitee, 0, 0)
			autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE kind='invitation' AND status='paid'", 0)
		})
	}
	t.Run("blocked_invitation_batch_cannot_starve_payable_rows", func(t *testing.T) {
		db, s, inviter, invitee := setup(t, false)
		setNewcomer(t, s, off)
		invitationSQL(t, db, `INSERT INTO qq_activity_rewards(kind,recipient_qq,recipient_user_id,subject_qq,subject_user_id) SELECT 'invitation','12345678',$1,(40000000+i)::text,10000+i FROM generate_series(1,105) i`, inviter)
		process(t, s, invitationJoin("new-join", "23456789", "12345678"))
		setNewcomer(t, s, on)
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='pending'", 105)
		autoCount(t, db, "SELECT count(*) FROM user_affiliate_ledger", 1)
	})
	t.Run("paused_campaign_reopens_with_both_payments_in_order", func(t *testing.T) {
		db, s, inviter, invitee := setup(t, false)
		if err := s.Control(ctx, "fixture", "paused"); err != nil {
			t.Fatal(err)
		}
		process(t, s, invitationJoin("paused-join", "23456789", "12345678"))
		invitationMoney(t, db, inviter, invitee, 0, 0)
		if err := s.Control(ctx, "fixture", "active"); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 2, 2)
	})
	t.Run("paid_newcomer_waits_for_inviter_binding_even_after_end", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		autoInitialize(t, s, "12345678")
		invitationSQL(t, db, "UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'")
		invitee := autoAdd(t, db, "23456789@qq.com", "")
		process(t, s, invitationJoin("new-join", "23456789", "12345678"))
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE kind='newcomer' AND status='paid'", 1)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_invitations WHERE status='awaiting_inviter'", 1)
		if err := s.Control(ctx, "fixture", "ended"); err != nil {
			t.Fatal(err)
		}
		inviter := autoAdd(t, db, "12345678@qq.com", "")
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, "SELECT balance::integer FROM users WHERE id=$1", 0, inviter)
	})
	t.Run("payout_failure_rolls_back_newcomer_invitation_and_notices", func(t *testing.T) {
		db, s, inviter, invitee := setup(t, false)
		setNewcomer(t, s, off)
		process(t, s, invitationJoin("new-join", "23456789", "12345678"))
		invitationSQL(t, db, `CREATE FUNCTION reject_invitation_payment() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture invitation payment unavailable'; END $$;
			CREATE TRIGGER reject_invitation_payment BEFORE INSERT ON user_affiliate_ledger FOR EACH ROW EXECUTE FUNCTION reject_invitation_payment()`)
		err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{NewcomerEnabled: &on})
		if err == nil || !strings.Contains(err.Error(), "fixture invitation payment unavailable") {
			t.Fatalf("expected fixture payment failure: %v", err)
		}
		t.Logf("expected transaction failure: %v", err)
		invitationMoney(t, db, inviter, invitee, 0, 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='paid'", 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 0)
		invitationSQL(t, db, "DROP TRIGGER reject_invitation_payment ON user_affiliate_ledger")
		setNewcomer(t, s, on)
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, "SELECT count(*) FROM user_affiliate_ledger", 1)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 2)
	})
}
