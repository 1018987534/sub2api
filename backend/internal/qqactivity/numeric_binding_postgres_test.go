package qqactivity

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPostgresNumericQQAndPaymentNotices(t *testing.T) {
	ctx := context.Background()
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
