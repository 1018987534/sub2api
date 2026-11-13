package qqactivity

import (
	"context"
	"strings"
	"testing"
	"time"
)

func privateTimeoutReply(t *testing.T, c *privateConversation, e PrivateEvent) string {
	t.Helper()
	var reply string
	if err := c.db.QueryRow(`SELECT text FROM qq_activity_outbox WHERE dedup_key=$1`, "private:"+e.ID).Scan(&reply); err != nil {
		t.Fatal(err)
	}
	t.Logf("reply=%s; mail_sends=%d", reply, c.sends)
	return reply
}

func requirePrivateTimeoutReply(t *testing.T, reply string) {
	t.Helper()
	if !strings.Contains(reply, "过期") || !strings.Contains(reply, "重新") {
		t.Errorf("missing expiry explanation and restart instructions: %q", reply)
	}
}

func TestPostgresPrivateTimeoutRecovery(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		name := "before_cleanup"
		if cleanup {
			name = "after_cleanup"
		}
		t.Run(name, func(t *testing.T) {
			db, s := privateFixture(t)
			id := autoAdd(t, db, "member@163.com", "")
			c := privateMember(t, db, s, "12345001")
			c.send(t, "绑定")
			c.send(t, "member@163.com")
			invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET expires_at=NOW()-INTERVAL '1 second'`)
			if cleanup {
				if err := s.Tick(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			e := c.send(t, "确认")
			requirePrivateTimeoutReply(t, privateTimeoutReply(t, c, e))
			if c.sends != 0 {
				t.Fatal("expired confirmation sent mail")
			}
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions`, 0)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 0)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards`, 0)
			c.send(t, "member@163.com")
			confirm := c.send(t, "确认")
			if err := s.ProcessPrivate(context.Background(), confirm, c.mail); err != nil {
				t.Fatal(err)
			}
			if c.sends != 1 {
				t.Fatalf("restart or duplicate confirmation sent %d mails", c.sends)
			}
			c.send(t, c.code)
			c.bound(t, id)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards WHERE status='paid'`, 1)
			t.Log("restart completed; one email and one newcomer payment")
		})
	}
}

func TestPostgresPrivateTimeoutMissingConversation(t *testing.T) {
	for _, text := range []string{"确认", "确认绑定", "重发", "更换", "确认更换", "是", "123456"} {
		t.Run(text, func(t *testing.T) {
			db, s := privateFixture(t)
			c := privateMember(t, db, s, "12345001")
			requirePrivateTimeoutReply(t, privateTimeoutReply(t, c, c.send(t, text)))
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions`, 0)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_mail_sends`, 0)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 0)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards`, 0)
		})
	}
}

func TestPostgresPrivateTimeoutConfirmationWindow(t *testing.T) {
	db, s := privateFixture(t)
	_ = autoAdd(t, db, "member@163.com", "")
	c := privateMember(t, db, s, "12345001")
	c.send(t, "绑定")
	invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET expires_at=NOW()+INTERVAL '30 seconds'`)
	before := time.Now()
	e := c.send(t, "member@163.com")
	var expires time.Time
	if err := db.QueryRow(`SELECT expires_at FROM qq_activity_private_sessions WHERE qq=$1`, c.qq).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	fullWindow := !expires.Before(before.Add(10*time.Minute)) && !expires.After(time.Now().Add(10*time.Minute))
	t.Logf("email_confirmation_has_full_ten_minutes=%t", fullWindow)
	if !fullWindow {
		t.Error("email confirmation inherited the earlier step deadline")
	}
	if reply := privateTimeoutReply(t, c, e); !strings.Contains(reply, "10 分钟") {
		t.Error("confirmation prompt does not explain its deadline")
	}
	c.send(t, "确认")
	var sent time.Time
	if err := db.QueryRow(`SELECT expires_at,sent_at FROM qq_activity_private_sessions WHERE qq=$1`, c.qq).Scan(&expires, &sent); err != nil {
		t.Fatal(err)
	}
	if remaining := expires.Sub(sent); remaining < 9*time.Minute+59*time.Second || remaining > 10*time.Minute {
		t.Fatalf("OTP validity changed: %s", remaining)
	}
}

func TestPostgresPrivateTimeoutEmailPromptWindow(t *testing.T) {
	db, s := privateFixture(t)
	c := privateMember(t, db, s, "12345001")
	c.send(t, "绑定")
	invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET expires_at=NOW()+INTERVAL '30 seconds'`)
	before := time.Now()
	reply := privateTimeoutReply(t, c, c.send(t, "邮箱填错了"))
	var expires time.Time
	if err := db.QueryRow(`SELECT expires_at FROM qq_activity_private_sessions WHERE qq=$1`, c.qq).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "10 分钟") || expires.Before(before.Add(10*time.Minute)) || expires.After(time.Now().Add(10*time.Minute)) {
		t.Error("email prompt deadline does not give the promised ten minutes")
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_mail_sends`, 0)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 0)
}

func TestPostgresPrivateTimeoutFreshEmailRestarts(t *testing.T) {
	db, s := privateFixture(t)
	_ = autoAdd(t, db, "member@163.com", "")
	c := privateMember(t, db, s, "12345001")
	c.send(t, "member@163.com")
	invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET expires_at=NOW()-INTERVAL '1 second'`)
	reply := privateTimeoutReply(t, c, c.send(t, "member@163.com"))
	if !strings.Contains(reply, "是否绑定官网账号") {
		t.Errorf("fresh email was discarded after expiry: %q", reply)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions WHERE step='confirm_email' AND expires_at>NOW()`, 1)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_mail_sends`, 0)
}

func TestPostgresPrivateTimeoutRejectsExpiredCode(t *testing.T) {
	db, s := privateFixture(t)
	old := autoAdd(t, db, "old@example.test", "")
	_ = autoAdd(t, db, "next@example.test", "")
	c := privateMember(t, db, s, "12345001")
	c.bind(t, "old@example.test")
	c.age(t)
	c.begin(t, "next@example.test")
	invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET expires_at=NOW()-INTERVAL '1 second'`)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	requirePrivateTimeoutReply(t, privateTimeoutReply(t, c, c.send(t, c.code)))
	c.bound(t, old)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions`, 0)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards WHERE status='paid'`, 1)
	if c.sends != 2 {
		t.Fatal("expired code resent mail")
	}
}
