package qqactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestPostgresPaymentNoticeDurability(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*sql.DB, *Store, int64, int64) {
		t.Helper()
		db, s := autoBindingFixture(t)
		inviter := autoAdd(t, db, "12345678@qq.com", "")
		autoInitialize(t, s, "12345678")
		invitationSQL(t, db, "UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'")
		invitationSQL(t, db, "UPDATE qq_activity_members SET qq_level=7 WHERE qq='12345678'")
		invitee := autoAdd(t, db, "23456789@qq.com", "")
		return db, s, inviter, invitee
	}
	pay := func(t *testing.T, s *Store) {
		t.Helper()
		if err := s.Process(ctx, invitationJoin("notice-join", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
	}
	assertNotices := func(t *testing.T, db *sql.DB) {
		t.Helper()
		var paid, notices int
		if err := db.QueryRow(`SELECT count(*),count(o.id) FROM qq_activity_rewards r LEFT JOIN qq_activity_outbox o ON o.dedup_key='reward:'||r.id::text WHERE r.status='paid'`).Scan(&paid, &notices); err != nil {
			t.Fatal(err)
		}
		t.Logf("actual_paid_rewards=%d reward_notices=%d", paid, notices)
		if paid != 2 || notices != paid {
			t.Fatalf("each actual payment needs one notice: paid=%d notices=%d", paid, notices)
		}
	}
	t.Run("payment_notices_ignore_ordinary_message_mute", func(t *testing.T) {
		db, s, inviter, invitee := setup(t)
		off := false
		if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{GroupMessagesEnabled: &off}); err != nil {
			t.Fatal(err)
		}
		pay(t, s)
		invitationMoney(t, db, inviter, invitee, 2, 2)
		assertNotices(t, db)
		autoCount(t, db, `SELECT count(*) FROM qq_activity_outbox WHERE dedup_key NOT LIKE 'reward:%'`, 0)
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		pay(t, s)
		assertNotices(t, db)
	})
	t.Run("pending_notices_survive_mute_and_eight_failures", func(t *testing.T) {
		db, s, _, _ := setup(t)
		pay(t, s)
		off := false
		if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{GroupMessagesEnabled: &off}); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, `SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%' AND delivered_at IS NULL`, 2)
		invitationSQL(t, db, `UPDATE qq_activity_outbox SET attempts=8,next_attempt_at=NOW()-INTERVAL '1 second' WHERE dedup_key LIKE 'reward:%'`)
		notices, err := s.Outbox(ctx)
		if err != nil || len(notices) != 2 {
			t.Fatalf("paid notices must remain retryable after eight failures: count=%d err=%v", len(notices), err)
		}
		for _, notice := range notices {
			data, _ := json.Marshal(notice)
			var fields map[string]any
			_ = json.Unmarshal(data, &fields)
			if fields["reward"] != true || !strings.Contains(notice.Text, "$2") || !strings.Contains(notice.Text, "已到账") {
				t.Fatalf("payment notice lost its type or actual payout text: %s", data)
			}
		}
		if err = s.Acknowledge(ctx, notices[0].ID, false); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, `SELECT count(*) FROM qq_activity_outbox WHERE id=$1 AND attempts=9 AND delivered_at IS NULL AND next_attempt_at>NOW()`, 1, notices[0].ID)
		if err = s.Acknowledge(ctx, notices[0].ID, true); err != nil {
			t.Fatal(err)
		}
		if err = s.Acknowledge(ctx, notices[0].ID, true); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, `SELECT count(*) FROM qq_activity_outbox WHERE id=$1 AND attempts=10 AND delivered_at IS NOT NULL`, 1, notices[0].ID)
		t.Log("retry=9 remains pending; successful ack=10 completes once")
	})
	t.Run("departed_inviter_uses_invitees_activity_group", func(t *testing.T) {
		db, s, inviter, invitee := setup(t)
		invitationSQL(t, db, `UPDATE qq_activity_members SET in_group=FALSE WHERE qq='12345678'`)
		pay(t, s)
		invitationMoney(t, db, inviter, invitee, 2, 2)
		assertNotices(t, db)
		autoCount(t, db, `SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%' AND group_id='87654321'`, 2)
	})
	t.Run("failed_notice_insert_rolls_back_money", func(t *testing.T) {
		db, s, inviter, invitee := setup(t)
		invitationSQL(t, db, `CREATE FUNCTION reject_reward_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.dedup_key LIKE 'reward:%' THEN RAISE EXCEPTION 'fixture notice persistence failed'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_reward_notice BEFORE INSERT ON qq_activity_outbox FOR EACH ROW EXECUTE FUNCTION reject_reward_notice()`)
		if err := s.Process(ctx, invitationJoin("rejected-notice", "23456789", "12345678")); err == nil {
			t.Fatal("payment committed without its required durable notice")
		}
		invitationMoney(t, db, inviter, invitee, 0, 0)
		autoCount(t, db, `SELECT count(*) FROM qq_activity_rewards`, 0)
		autoCount(t, db, `SELECT count(*) FROM qq_activity_outbox`, 0)
		t.Log("notice insert failure: newcomer=0 inviter=0 rewards=0 outbox=0")
	})
}
