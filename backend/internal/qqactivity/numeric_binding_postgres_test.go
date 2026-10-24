package qqactivity

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestPostgresNumericQQAndPaymentNotices(t *testing.T) {
	ctx := context.Background()
	t.Run("both_username_formats_bind_automatically", func(t *testing.T) {
		db, store := autoBindingFixture(t)
		autoAdd(t, db, "12345101@qq.com", "12345101")
		autoAdd(t, db, "number@example.test", " 12345102 ")
		autoAdd(t, db, "mail@example.test", " 12345103@QQ.COM ")
		autoInitialize(t, store, "12345101", "12345102", "12345103", "12345104")
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 3)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE qq='12345101' AND source='matched_email'", 1)
		id := autoAdd(t, db, "later@example.test", "")
		if _, err := db.Exec("UPDATE users SET username='12345104' WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		if err := store.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 4)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_claims", 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards", 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox", 0)
	})
	t.Run("numeric_username_command_and_permanent_ownership", func(t *testing.T) {
		db, store := autoBindingFixture(t)
		owner := autoAdd(t, db, "owner@example.test", "12345105")
		other := autoAdd(t, db, "other@example.test", "")
		autoInitialize(t, store)
		level := 7
		err := store.Process(ctx, Event{ID: "number-bind", GroupID: "87654321", QQ: "12345105", Kind: "command", Command: "绑定", Time: time.Now().Unix(), Level: &level, MemberConfirmed: true})
		if err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE qq='12345105'", 1)
		if _, err = db.Exec("UPDATE users SET username='' WHERE id=$1", owner); err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec("UPDATE users SET username='12345105@qq.com' WHERE id=$1", other)
		if pe, ok := err.(*pq.Error); !ok || pe.Code != "23505" {
			t.Fatalf("permanently bound numeric QQ reused through email alias: %v", err)
		}
	})
	t.Run("cross_format_unique_and_same_owner_can_change_format", func(t *testing.T) {
		db, _ := autoBindingFixture(t)
		owner := autoAdd(t, db, "12345106@qq.com", "")
		other := autoAdd(t, db, "unique@example.test", "")
		for _, username := range []string{"12345106", " 12345106@QQ.COM "} {
			_, err := db.Exec("UPDATE users SET username=$1 WHERE id=$2", username, other)
			if pe, ok := err.(*pq.Error); !ok || pe.Code != "23505" {
				t.Errorf("cross-account alias %q was not rejected: %v", username, err)
			}
			if _, err = db.Exec("UPDATE users SET username=$1 WHERE id=$2", username, owner); err != nil {
				t.Fatal("own identity format change rejected", err)
			}
		}
		autoAdd(t, db, "numeric-owner@example.test", "12345107")
		_, err := db.Exec("INSERT INTO users(email) VALUES('12345107@qq.com')")
		if pe, ok := err.(*pq.Error); !ok || pe.Code != "23505" {
			t.Fatalf("QQ registration bypassed numeric alias reservation: %v", err)
		}
	})
	t.Run("concurrent_number_and_email_alias_have_one_owner", func(t *testing.T) {
		db, _ := autoBindingFixture(t)
		ids := []int64{autoAdd(t, db, "race-number@example.test", ""), autoAdd(t, db, "race-email@example.test", "")}
		values := []string{"12345108", "12345108@qq.com"}
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := db.Exec("UPDATE users SET username=$1 WHERE id=$2", values[i], ids[i])
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		success, rejected := 0, 0
		for err := range errs {
			if err == nil {
				success++
			} else if pe, ok := err.(*pq.Error); ok && pe.Code == "23505" {
				rejected++
			} else {
				t.Fatal(err)
			}
		}
		t.Logf("numeric/email concurrent success=%d unique_rejections=%d", success, rejected)
		if success != 1 || rejected != 1 {
			t.Fail()
		}
	})
	t.Run("ordinary_replies_and_exactly_once_paid_notices", func(t *testing.T) {
		db, store := autoBindingFixture(t)
		autoAdd(t, db, "12345109@qq.com", "")
		autoInitialize(t, store, "12345109")
		if _, err := db.Exec("UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'"); err != nil {
			t.Fatal(err)
		}
		process := func(id, qq, kind, inviter string, level int) {
			t.Helper()
			e := Event{ID: id, GroupID: "87654321", QQ: qq, Kind: kind, Command: "绑定", Time: time.Now().Unix(), Level: &level, MemberConfirmed: true}
			if inviter != "" {
				e.InviterQQ, e.EvidenceType, e.EvidenceKey = inviter, "direct_invite_pinned", "fixture:"+id
			}
			if err := store.Process(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		assertReply := func(id, want string) {
			t.Helper()
			var got string
			if err := db.QueryRow("SELECT text FROM qq_activity_outbox WHERE dedup_key=$1", "event:"+id).Scan(&got); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s reply=%q", id, got)
			if got != want || strings.Contains(got, "$2") {
				t.Errorf("ordinary reply=%q want=%q", got, want)
			}
		}
		process("old-bind", "12345109", "command", "", 7)
		assertReply("old-bind", "你已经绑定官网账号：12****09@qq.com。")
		autoAdd(t, db, "12345110@qq.com", "")
		process("new-join", "12345110", "join", "12345109", 7)
		assertReply("new-join", "绑定成功！已关联官网账号：12****10@qq.com。")
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 0)
		process("level-ready-bind", "12345110", "command", "", 16)
		assertReply("level-ready-bind", "你已经绑定官网账号：12****10@qq.com。")
		for _, kind := range []string{"newcomer", "invitation"} {
			var text string
			err := db.QueryRow("SELECT o.text FROM qq_activity_rewards r JOIN qq_activity_outbox o ON o.dedup_key='reward:'||r.id::text WHERE r.kind=$1 AND r.subject_qq='12345110' AND r.status='paid'", kind).Scan(&text)
			if err != nil || !strings.Contains(text, "$2") || !strings.Contains(text, "已到账") {
				t.Fatalf("actual %s payout notice missing: %q %v", kind, text, err)
			}
			t.Logf("newly_paid_%s_notice=%q", kind, text)
		}
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("repeat-%d", i)
			process(id, "12345110", "command", "", 16)
			assertReply(id, "你已经绑定官网账号：12****10@qq.com。")
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='paid'", 2)
		if err := store.Control(ctx, "fixture", "paused"); err != nil {
			t.Fatal(err)
		}
		autoAdd(t, db, "12345111@qq.com", "")
		process("paused-join", "12345111", "join", "12345109", 16)
		assertReply("paused-join", "绑定成功！已关联官网账号：12****11@qq.com。")
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 2)
		if err := store.Control(ctx, "fixture", "active"); err != nil {
			t.Fatal(err)
		}
		if err := store.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 4)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='paid'", 4)
	})
}
