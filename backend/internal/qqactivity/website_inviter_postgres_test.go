package qqactivity

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
)

func websiteInviterFixture(t *testing.T, websiteBound bool) (*sql.DB, *Store, int64, int64, int64) {
	t.Helper()
	db, s := autoBindingFixture(t)
	website := autoAdd(t, db, "34567890@qq.com", "")
	group := autoAdd(t, db, "12345678@qq.com", "")
	members := []string{"12345678"}
	if websiteBound {
		members = append(members, "34567890")
	}
	autoInitialize(t, s, members...)
	invitationSQL(t, db, "UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'")
	invitationSQL(t, db, "UPDATE qq_activity_members SET qq_level=7 WHERE qq IN ('12345678','34567890')")
	newcomer := autoAdd(t, db, "23456789@qq.com", "")
	return db, s, website, group, newcomer
}

func websiteRelation(t *testing.T, db *sql.DB, newcomer, inviter int64) {
	t.Helper()
	invitationSQL(t, db, `INSERT INTO user_affiliates(user_id,inviter_id,aff_code) VALUES($1,$2,'fixture-newcomer') ON CONFLICT(user_id) DO UPDATE SET inviter_id=EXCLUDED.inviter_id`, newcomer, inviter)
}

func TestPostgresWebsiteInviterPriority(t *testing.T) {
	ctx := context.Background()
	db, s, website, group, newcomer := websiteInviterFixture(t, true)
	websiteRelation(t, db, newcomer, website)
	invitationSQL(t, db, `CREATE FUNCTION require_paid_newcomer() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NOT EXISTS(SELECT 1 FROM qq_activity_rewards WHERE kind='newcomer' AND status='paid' AND subject_user_id=NEW.source_user_id)
		THEN RAISE EXCEPTION 'invitation preceded newcomer payment'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER require_paid_newcomer BEFORE INSERT ON user_affiliate_ledger FOR EACH ROW EXECUTE FUNCTION require_paid_newcomer()`)
	if err := s.Process(ctx, invitationJoin("website-priority", "23456789", "12345678")); err != nil {
		t.Fatal(err)
	}
	var balance, websiteQuota, groupQuota float64
	if err := db.QueryRow(`SELECT balance,COALESCE((SELECT aff_quota FROM user_affiliates WHERE user_id=$1),0),COALESCE((SELECT aff_quota FROM user_affiliates WHERE user_id=$2),0) FROM users WHERE id=$3`, website, group, newcomer).Scan(&balance, &websiteQuota, &groupQuota); err != nil {
		t.Fatal(err)
	}
	t.Logf("newcomer_balance=%.0f website_inviter_quota=%.0f qq_inviter_quota=%.0f", balance, websiteQuota, groupQuota)
	if balance != 2 || websiteQuota != 2 || groupQuota != 0 {
		t.Fatalf("website inviter must take priority: newcomer/website/qq=%v/%v/%v", balance, websiteQuota, groupQuota)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.Process(ctx, invitationJoin(fmt.Sprintf("website-repeat-%d", i), "23456789", "45678901"))
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
	invitationMoney(t, db, website, newcomer, 2, 2)
	invitationMoney(t, db, group, newcomer, 2, 0)
	autoCount(t, db, "SELECT count(*) FROM user_affiliate_ledger", 1)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='paid'", 2)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 2)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE qq='34567890' AND text LIKE '%邀请余额已到账%'", 1)
	// The original QQ invitation remains available as fallback evidence.
	autoCount(t, db, "SELECT count(*) FROM qq_activity_invitations WHERE inviter_qq='12345678' AND inviter_user_id=$1", 1, group)
	report, err := s.AdminReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Invitations) != 1 || report.Invitations[0].Inviter != "34567890" || report.Invitations[0].Evidence != "website_affiliate" {
		t.Fatalf("report must show the actual website recipient: %+v", report.Invitations)
	}
	summary, err := s.UserSummary(ctx, website)
	if err != nil || summary.InvitationPaid != 2 {
		t.Fatalf("website inviter summary=%+v err=%v", summary, err)
	}
}

func TestPostgresWebsiteInviterFallbackAndBinding(t *testing.T) {
	ctx := context.Background()
	t.Run("website_without_qq_invitation", func(t *testing.T) {
		db, s, website, group, newcomer := websiteInviterFixture(t, true)
		websiteRelation(t, db, newcomer, website)
		if err := s.Process(ctx, invitationJoin("no-qq-evidence", "23456789", "")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 2)
		invitationMoney(t, db, group, newcomer, 2, 0)
	})
	t.Run("qq_fallback_only_without_website_inviter", func(t *testing.T) {
		db, s, website, group, newcomer := websiteInviterFixture(t, true)
		if err := s.Process(ctx, invitationJoin("qq-fallback", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 0)
		invitationMoney(t, db, group, newcomer, 2, 2)
		// A later website relationship cannot pay a second inviter or move history.
		websiteRelation(t, db, newcomer, website)
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 0)
		invitationMoney(t, db, group, newcomer, 2, 2)
		autoCount(t, db, "SELECT count(*) FROM user_affiliate_ledger", 1)
	})
	t.Run("website_binding_required_without_qq_fallback", func(t *testing.T) {
		db, s, website, group, newcomer := websiteInviterFixture(t, false)
		websiteRelation(t, db, newcomer, website)
		if err := s.Process(ctx, invitationJoin("unbound-website", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 0)
		invitationMoney(t, db, group, newcomer, 2, 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_invitations WHERE status='awaiting_inviter'", 1)
		e := invitationJoin("website-binds-later", "34567890", "")
		level := 7
		e.Level = &level
		if err := s.Process(ctx, e); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 2)
		invitationMoney(t, db, group, newcomer, 2, 0)
	})
	t.Run("locked_website_binding_cannot_fall_back", func(t *testing.T) {
		db, s, website, group, newcomer := websiteInviterFixture(t, true)
		websiteRelation(t, db, newcomer, website)
		if err := s.LockBinding(ctx, "fixture", "34567890", "fixture binding locked"); err != nil {
			t.Fatal(err)
		}
		if err := s.Process(ctx, invitationJoin("locked-website", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 0)
		invitationMoney(t, db, group, newcomer, 2, 0)
	})
	t.Run("self_website_relation_cannot_fall_back", func(t *testing.T) {
		db, s, _, group, newcomer := websiteInviterFixture(t, true)
		websiteRelation(t, db, newcomer, newcomer)
		if err := s.Process(ctx, invitationJoin("self-website", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, group, newcomer, 2, 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE kind='invitation' AND status='paid'", 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_invitations WHERE status='ineligible'", 1)
	})
	t.Run("select_website_only_after_newcomer_payment", func(t *testing.T) {
		db, s, website, group, newcomer := websiteInviterFixture(t, true)
		off, on := false, true
		if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{NewcomerEnabled: &off}); err != nil {
			t.Fatal(err)
		}
		if err := s.Process(ctx, invitationJoin("newcomer-disabled", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		websiteRelation(t, db, newcomer, website)
		invitationMoney(t, db, website, newcomer, 0, 0)
		invitationMoney(t, db, group, newcomer, 0, 0)
		if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{NewcomerEnabled: &on}); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 2)
		invitationMoney(t, db, group, newcomer, 2, 0)
	})
	t.Run("pending_website_relation_removed_uses_original_qq", func(t *testing.T) {
		db, s, website, group, newcomer := websiteInviterFixture(t, true)
		websiteRelation(t, db, newcomer, website)
		off, on := false, true
		if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{InvitationEnabled: &off}); err != nil {
			t.Fatal(err)
		}
		if err := s.Process(ctx, invitationJoin("invitation-disabled", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationSQL(t, db, "UPDATE user_affiliates SET inviter_id=NULL WHERE user_id=$1", newcomer)
		if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{InvitationEnabled: &on}); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, website, newcomer, 2, 0)
		invitationMoney(t, db, group, newcomer, 2, 2)
	})
}
