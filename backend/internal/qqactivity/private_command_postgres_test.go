package qqactivity

import (
	"context"
	"testing"
	"time"
)

func TestPostgresPrivateRequiresExplicitBinding(t *testing.T) {
	for _, state := range []string{"idle", "nonmember", "bound", "locked", "expired", "cancelled", "completed"} {
		t.Run(state, func(t *testing.T) {
			db, s := privateFixture(t)
			id := autoAdd(t, db, "member@example.test", "")
			c := privateMember(t, db, s, "12345001")
			if state == "bound" || state == "locked" {
				invitationSQL(t, db, `INSERT INTO qq_activity_bindings(qq,user_id,source) VALUES($1,$2,'verified_email')`, c.qq, id)
			}
			if state == "locked" {
				invitationSQL(t, db, `UPDATE qq_activity_bindings SET status='locked' WHERE qq=$1`, c.qq)
			}
			if state == "completed" {
				c.bind(t, "member@example.test")
			}
			for _, input := range []string{"你好", "nono", "member@example.test", "123456", "确认", "重发", "取消", "更换绑定", "请绑定", "绑定状态", "绑定 member@example.test", ""} {
				invitationSQL(t, db, `DELETE FROM qq_activity_private_sessions WHERE qq=$1`, c.qq)
				if state == "expired" || state == "cancelled" {
					c.send(t, "绑定")
					if state == "expired" {
						invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET expires_at=NOW()-INTERVAL '1 second' WHERE qq=$1`, c.qq)
					} else {
						c.send(t, "取消")
					}
				}
				var before, after, active int
				if err := db.QueryRow(`SELECT COUNT(*) FROM qq_activity_outbox WHERE scope='private' AND qq=$1`, c.qq).Scan(&before); err != nil {
					t.Fatal(err)
				}
				sends := c.sends
				if state == "nonmember" {
					c.n++
					if err := s.ProcessPrivate(context.Background(), PrivateEvent{ID: "nonmember:" + input, QQ: c.qq, Text: input, Time: time.Now().Unix()}, c.mail); err != nil {
						t.Fatal(err)
					}
				} else {
					c.send(t, input)
				}
				if err := db.QueryRow(`SELECT COUNT(*) FROM qq_activity_outbox WHERE scope='private' AND qq=$1`, c.qq).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`SELECT COUNT(*) FROM qq_activity_private_sessions WHERE qq=$1 AND expires_at>NOW()`, c.qq).Scan(&active); err != nil {
					t.Fatal(err)
				}
				t.Logf("input=%q replies=%d active_sessions=%d emails=%d", input, after-before, active, c.sends-sends)
				if after != before || active != 0 || c.sends != sends {
					t.Errorf("input %q triggered binding without the explicit command", input)
				}
			}
		})
	}
}

func TestPostgresPrivateBindingCommandStarts(t *testing.T) {
	for _, input := range []string{"绑定", " \t绑定\n"} {
		t.Run(input, func(t *testing.T) {
			db, s := privateFixture(t)
			c := privateMember(t, db, s, "12345001")
			c.send(t, input)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions WHERE qq=$1 AND step='email'`, 1, c.qq)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_outbox WHERE scope='private' AND qq=$1`, 1, c.qq)
		})
	}
}
