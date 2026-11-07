package qqactivity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/migrations"
	"strings"
	"sync"
	"testing"
	"time"
)

type privateConversation struct {
	db          *sql.DB
	s           *Store
	qq          string
	n           int
	code, email string
	sends       int
	fail        bool
}

func privateFixture(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	db, s := autoBindingFixture(t)
	autoInitialize(t, s)
	invitationSQL(t, db, `UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'`)
	return db, s
}

func privateMember(t *testing.T, db *sql.DB, s *Store, qq string) *privateConversation {
	t.Helper()
	if err := s.Process(context.Background(), invitationJoin("join:"+qq, qq, "")); err != nil {
		t.Fatal(err)
	}
	return &privateConversation{db: db, s: s, qq: qq}
}

func (c *privateConversation) send(t *testing.T, text string) PrivateEvent {
	t.Helper()
	c.n++
	e := PrivateEvent{ID: fmt.Sprintf("dm:%s:%d", c.qq, c.n), QQ: c.qq, Text: text, Time: time.Now().Unix(), GroupID: "87654321", MemberConfirmed: true}
	if err := c.s.ProcessPrivate(context.Background(), e, c.mail); err != nil {
		t.Fatal(err)
	}
	return e
}
func (c *privateConversation) mail(_ context.Context, email, code string) error {
	c.sends++
	c.email, c.code = email, code
	if c.fail {
		return errors.New("fixture SMTP failure")
	}
	return nil
}
func (c *privateConversation) begin(t *testing.T, email string) {
	t.Helper()
	var bound bool
	if err := c.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM qq_activity_bindings WHERE qq=$1)`, c.qq).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	c.send(t, "绑定")
	if bound {
		c.send(t, "更换")
	}
	c.send(t, email)
	c.send(t, "确认")
}
func (c *privateConversation) bind(t *testing.T, email string) {
	t.Helper()
	c.begin(t, email)
	c.send(t, c.code)
	var step string
	err := c.db.QueryRow(`SELECT step FROM qq_activity_private_sessions WHERE qq=$1`, c.qq).Scan(&step)
	if err == nil && step == "confirm_switch" {
		c.send(t, "确认更换")
	}
}
func (c *privateConversation) age(t *testing.T) {
	t.Helper()
	invitationSQL(t, c.db, `UPDATE qq_activity_private_mail_sends SET created_at=created_at-INTERVAL '61 seconds'`)
}
func (c *privateConversation) bound(t *testing.T, user int64) {
	t.Helper()
	autoCount(t, c.db, `SELECT COUNT(*) FROM qq_activity_bindings WHERE qq=$1 AND user_id=$2`, 1, c.qq, user)
}

func TestPostgresPrivateFirstBinding(t *testing.T) {
	for _, email := range []string{"custom.alias@qq.com", "member@gmail.com", "member@163.com", "87654001@qq.com"} {
		t.Run(email, func(t *testing.T) {
			db, s := privateFixture(t)
			id := autoAdd(t, db, email, "unchanged-profile")
			c := privateMember(t, db, s, "12345001")
			c.send(t, " "+strings.ToUpper(email)+" ")
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 0)
			if c.sends != 0 {
				t.Fatal("mail sent before confirmation")
			}
			e := c.send(t, "确认")
			if c.email != email || !codePattern.MatchString(c.code) {
				t.Fatal("wrong registered mailbox or code")
			}
			if err := s.ProcessPrivate(context.Background(), e, c.mail); err != nil {
				t.Fatal(err)
			}
			if c.sends != 1 {
				t.Fatal("duplicate event sent another code")
			}
			// Restarting the store preserves the conversation.
			c.s = NewStore(db, nil)
			c.send(t, c.code)
			c.bound(t, id)
			autoCount(t, db, `SELECT COUNT(*) FROM users WHERE id=$1 AND email=$2 AND username='unchanged-profile'`, 1, id, email)
			autoCount(t, db, `SELECT balance::int FROM users WHERE id=$1`, 2, id)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards WHERE kind='newcomer' AND status='paid'`, 1)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_ever_bound WHERE identity_kind='email' AND identity_key=$1`, 1, Hash(email))
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions`, 0)
			// No plaintext OTP in any database event, audit, or outbox content.
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_outbox WHERE text LIKE '%'||$1||'%'`, 0, c.code)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_audit WHERE detail::text LIKE '%'||$1||'%'`, 0, c.code)
			if err := s.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			autoCount(t, db, `SELECT balance::int FROM users WHERE id=$1`, 2, id)
			t.Log("private QQ identity and registered email independently verified; account fields unchanged; first reward paid once")
		})
	}
}

func TestPostgresPrivateChangeConfirmation(t *testing.T) {
	for _, action := range []string{"cancel_before_code", "cancel_after_code", "expire", "wrong_codes", "locked", "same_account", "email_changed", "binding_changed", "complete"} {
		t.Run(action, func(t *testing.T) {
			db, s := privateFixture(t)
			old := autoAdd(t, db, "old@example.test", "")
			next := autoAdd(t, db, "next@example.test", "")
			c := privateMember(t, db, s, "12345001")
			c.bind(t, "old@example.test")
			c.age(t)
			if action == "locked" {
				if err := s.LockBinding(context.Background(), "fixture", c.qq, "fixture locked binding"); err != nil {
					t.Fatal(err)
				}
				c.begin(t, "next@example.test")
				c.bound(t, old)
				autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions`, 0)
				return
			}
			if action == "same_account" {
				c.send(t, "绑定")
				c.send(t, "更换")
				c.send(t, "old@example.test")
				if c.sends != 1 {
					t.Fatal("same account triggered OTP")
				}
				c.bound(t, old)
				return
			}
			c.begin(t, "next@example.test")
			c.bound(t, old)
			if action == "cancel_before_code" {
				c.send(t, "取消")
				c.send(t, c.code)
				c.bound(t, old)
				return
			}
			if action == "expire" {
				invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET expires_at=NOW()-INTERVAL '1 second'`)
				c.send(t, c.code)
				c.bound(t, old)
				return
			}
			if action == "wrong_codes" {
				wrong := "000000"
				if c.code == wrong {
					wrong = "111111"
				}
				for i := 0; i < 5; i++ {
					c.send(t, wrong)
				}
				c.send(t, c.code)
				c.bound(t, old)
				autoCount(t, db, `SELECT attempts FROM qq_activity_private_sessions`, 5)
				return
			}
			c.send(t, c.code)
			c.bound(t, old) // Verification alone never switches an existing binding.
			if action == "cancel_after_code" {
				c.send(t, "取消")
				c.send(t, "确认更换")
				c.bound(t, old)
				return
			}
			if action == "email_changed" {
				invitationSQL(t, db, `UPDATE users SET email='changed@example.test' WHERE id=$1`, next)
			}
			if action == "binding_changed" {
				if err := s.Unbind(context.Background(), old); err != nil {
					t.Fatal(err)
				}
				c.send(t, "确认更换")
				autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 0)
				return
			}
			e := c.send(t, "确认更换")
			if action == "email_changed" {
				c.bound(t, old)
				return
			}
			c.bound(t, next)
			if err := s.ProcessPrivate(context.Background(), e, c.mail); err != nil {
				t.Fatal(err)
			}
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_binding_history`, 2)
			autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_binding_history WHERE unbound_at IS NOT NULL`, 1)
			autoCount(t, db, `SELECT balance::int FROM users WHERE id=$1`, 2, old)
			autoCount(t, db, `SELECT balance::int FROM users WHERE id=$1`, 0, next)
			view, err := s.UserSummary(context.Background(), next)
			if err != nil || len(view.Rewards) != 0 || view.WelcomeStatus == "paid" {
				t.Fatalf("replacement inherited previous payout: %+v %v", view, err)
			}
			view, err = s.UserSummary(context.Background(), old)
			if err != nil || len(view.Rewards) != 1 {
				t.Fatalf("original payout history disappeared: %+v %v", view, err)
			}
			other := privateMember(t, db, s, "12345002")
			other.age(t)
			other.bind(t, "old@example.test")
			view, err = s.UserSummary(context.Background(), old)
			if err != nil || len(view.Rewards) != 1 {
				t.Fatalf("history lost after association with another QQ: %+v %v", view, err)
			}
			t.Log("atomic switch after final confirmation; balances and reward ownership retained")
		})
	}
}

func TestPostgresPrivateNeverPaidCannotReopenEligibility(t *testing.T) {
	db, s := privateFixture(t)
	old := autoAdd(t, db, "old@example.test", "")
	next := autoAdd(t, db, "next@example.test", "")
	c := privateMember(t, db, s, "12345001")
	invitationSQL(t, db, `UPDATE qq_activity_members SET qq_level=15 WHERE qq=$1`, c.qq)
	c.bind(t, "old@example.test")
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards`, 0)
	c.age(t)
	c.bind(t, "next@example.test")
	level := 21
	if err := s.Process(context.Background(), Event{ID: "level-up", Kind: "level", GroupID: "87654321", QQ: c.qq, Time: time.Now().Unix(), Level: &level, MemberConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	c.bound(t, next)
	c2 := privateMember(t, db, s, "12345002")
	c2.age(t)
	c2.bind(t, "old@example.test")
	c2.bound(t, old)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards`, 0)
	if err := s.Unbind(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	_ = autoAdd(t, db, c2.qq+"@qq.com", "")
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings WHERE qq=$1`, 0, c2.qq)
	c2.age(t)
	c2.bind(t, "old@example.test")
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards`, 0)
	t.Log("never-paid QQ rebind, reused website account and unbind/rebind all remain ineligible")
}

func TestPostgresPrivateFirstBindingCanFinishOriginalLevelCheck(t *testing.T) {
	db, s := privateFixture(t)
	id := autoAdd(t, db, "new@example.test", "")
	c := privateMember(t, db, s, "12345001")
	invitationSQL(t, db, `UPDATE qq_activity_members SET qq_level=NULL WHERE qq=$1`, c.qq)
	c.bind(t, "new@example.test")
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards`, 0)
	level := 21
	if err := s.Process(context.Background(), Event{ID: "level-up", Kind: "level", GroupID: "87654321", QQ: c.qq, Time: time.Now().Unix(), Level: &level, MemberConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT balance::int FROM users WHERE id=$1`, 2, id)
}

func TestPostgresPrivateDeletedAccountEmailHistory(t *testing.T) {
	db, s := privateFixture(t)
	id := autoAdd(t, db, "original@example.test", "")
	c := privateMember(t, db, s, "12345001")
	invitationSQL(t, db, `UPDATE qq_activity_members SET qq_level=7 WHERE qq=$1`, c.qq)
	c.bind(t, "original@example.test")
	invitationSQL(t, db, `UPDATE users SET email='renamed@example.test' WHERE id=$1`, id)
	if err := s.Unbind(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	invitationSQL(t, db, `DELETE FROM users WHERE id=$1`, id)
	for i, email := range []string{"original@example.test", "renamed@example.test"} {
		newID := autoAdd(t, db, email, "")
		c2 := privateMember(t, db, s, fmt.Sprint(12345002+i))
		c2.age(t)
		c2.bind(t, email)
		c2.bound(t, newID)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards`, 0)
	t.Log("old and renamed mailbox digests survived physical account deletion; recreated accounts received no newcomer reward")
}

func TestPostgresPrivateCooldownFailureAndMembership(t *testing.T) {
	db, s := privateFixture(t)
	_ = autoAdd(t, db, "new@example.test", "")
	c := privateMember(t, db, s, "12345001")
	c.fail = true
	c.begin(t, "new@example.test")
	c.send(t, "重发")
	if c.sends != 1 {
		t.Fatal("cooldown bypassed after SMTP failure")
	}
	c.age(t)
	c.fail = false
	c.send(t, "重发")
	if c.sends != 2 {
		t.Fatal("resend did not recover")
	}
	code := c.code
	e := PrivateEvent{ID: "absent", QQ: c.qq, Text: code, Time: time.Now().Unix(), GroupID: "87654321", MemberConfirmed: false}
	if err := s.ProcessPrivate(context.Background(), e, c.mail); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 0)
	c.send(t, code)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 1)
	if err := s.FriendAdded(context.Background(), c.qq); err != nil {
		t.Fatal(err)
	}
	if err := s.FriendAdded(context.Background(), c.qq); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_outbox WHERE scope='private' AND dedup_key=$1`, 1, "friend:"+c.qq)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 1)
}

func TestPostgresPrivateConcurrentAccountConflict(t *testing.T) {
	db, s := privateFixture(t)
	id := autoAdd(t, db, "new@example.test", "")
	a, b := privateMember(t, db, s, "12345001"), privateMember(t, db, s, "12345002")
	a.begin(t, "new@example.test")
	a.age(t)
	b.begin(t, "new@example.test")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, c := range []*privateConversation{a, b} {
		wg.Add(1)
		go func(i int, c *privateConversation) {
			defer wg.Done()
			errs <- s.ProcessPrivate(context.Background(), PrivateEvent{ID: fmt.Sprint("race:", i), QQ: c.qq, Text: c.code, Time: time.Now().Unix(), GroupID: "87654321", MemberConfirmed: true}, nil)
		}(i, c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings WHERE user_id=$1`, 1, id)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_binding_history WHERE user_id=$1`, 1, id)
	autoCount(t, db, `SELECT balance::int FROM users WHERE id=$1`, 2, id)
}

func TestPostgresPrivatePendingRewardsKeepOriginalRecipients(t *testing.T) {
	ctx := context.Background()
	db, s := autoBindingFixture(t)
	inviter := autoAdd(t, db, "12345678@qq.com", "")
	autoInitialize(t, s, "12345678")
	invitationSQL(t, db, `UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds',newcomer_enabled=FALSE`)
	invitee := autoAdd(t, db, "23456789@qq.com", "")
	if err := s.Process(ctx, invitationJoin("invited", "23456789", "12345678")); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards WHERE status='pending'`, 2)
	newInviter := autoAdd(t, db, "new-inviter@example.test", "")
	newInvitee := autoAdd(t, db, "new-invitee@example.test", "")
	a := &privateConversation{db: db, s: s, qq: "12345678"}
	b := &privateConversation{db: db, s: s, qq: "23456789"}
	a.bind(t, "new-inviter@example.test")
	b.bind(t, "new-invitee@example.test")
	on := true
	if err := s.UpdateSettings(ctx, "fixture", SettingsUpdate{NewcomerEnabled: &on}); err != nil {
		t.Fatal(err)
	}
	invitationMoney(t, db, inviter, invitee, 2, 2)
	autoCount(t, db, `SELECT balance::int FROM users WHERE id=$1`, 0, newInvitee)
	autoCount(t, db, `SELECT COUNT(*) FROM user_affiliates WHERE user_id=$1`, 0, newInviter)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_invitations WHERE invitee_user_id=$1 AND inviter_user_id=$2 AND status='paid'`, 1, invitee, inviter)
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'`, 2)
	t.Log("both parties rebound before payment; newcomer and invitation paid once to original accounts; original invitation subject retained")
}

func TestPostgresPrivateWelcomeHistory(t *testing.T) {
	db, s := privateFixture(t)
	c := privateMember(t, db, s, "12345001")
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_outbox WHERE qq=$1 AND text=$2`, 1, c.qq, Prompt("M01", nil))
	if err := s.Process(context.Background(), invitationJoin("rejoin", c.qq, "")); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_outbox WHERE qq=$1 AND text=$2`, 1, c.qq, Prompt("M01", nil))
}

func TestPostgresPrivateInterruptedMailAndCancellation(t *testing.T) {
	db, s := privateFixture(t)
	id := autoAdd(t, db, "new@example.test", "")
	c := privateMember(t, db, s, "12345001")
	c.send(t, "new@example.test")
	err := s.ProcessPrivate(context.Background(), PrivateEvent{ID: "confirm-cancel", QQ: c.qq, Text: "确认", Time: time.Now().Unix(), GroupID: "87654321", MemberConfirmed: true}, func(ctx context.Context, email, code string) error {
		c.code = code
		// Proves SMTP does not hold the activity transaction/advisory lock.
		return s.ProcessPrivate(ctx, PrivateEvent{ID: "cancel-in-flight", QQ: c.qq, Text: "取消", Time: time.Now().Unix()}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions`, 0)
	c.send(t, c.code)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_bindings`, 0)
	c.age(t)
	c.begin(t, "new@example.test")
	invitationSQL(t, db, `UPDATE qq_activity_private_sessions SET step='sending_code',sent_at=NULL,updated_at=NOW()-INTERVAL '3 minutes'`)
	if err = s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_private_sessions WHERE step='confirm_email' AND code_hash=''`, 1)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'private-recover:%'`, 1)
	c.age(t)
	c.send(t, "重发")
	c.send(t, c.code)
	c.bound(t, id)
}

func TestPostgresPrivateHourlyLimitAndResend(t *testing.T) {
	db, s := privateFixture(t)
	_ = autoAdd(t, db, "new@example.test", "")
	c := privateMember(t, db, s, "12345001")
	c.begin(t, "new@example.test")
	for i := 0; i < 4; i++ {
		c.age(t)
		c.send(t, "重发")
	}
	c.age(t)
	c.send(t, "重发")
	if c.sends != 5 {
		t.Fatalf("hourly sends=%d", c.sends)
	}
	var expected, challenge string
	if err := db.QueryRow(`SELECT code_hash,challenge FROM qq_activity_private_sessions`).Scan(&expected, &challenge); err != nil {
		t.Fatal(err)
	}
	if expected != Hash("qq-private:"+challenge+":"+c.code) || expected == Hash(challenge+":"+c.code) {
		t.Fatal("private OTP purpose isolation failed")
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_mail_codes`, 0)
}

func TestPostgresPrivateSwitchFailureRollsBackOldBinding(t *testing.T) {
	db, s := privateFixture(t)
	old := autoAdd(t, db, "old@example.test", "")
	next := autoAdd(t, db, "next@example.test", "")
	c := privateMember(t, db, s, "12345001")
	c.bind(t, "old@example.test")
	c.age(t)
	c.begin(t, "next@example.test")
	c.send(t, c.code)
	invitationSQL(t, db, fmt.Sprintf(`CREATE FUNCTION reject_fixture_binding() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.user_id=%d THEN RAISE EXCEPTION 'fixture binding insert failed'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_fixture_binding BEFORE INSERT ON qq_activity_bindings FOR EACH ROW EXECUTE FUNCTION reject_fixture_binding()`, next))
	e := PrivateEvent{ID: "final-failed", QQ: c.qq, Text: "确认更换", Time: time.Now().Unix(), GroupID: "87654321", MemberConfirmed: true}
	err := s.ProcessPrivate(context.Background(), e, nil)
	if err == nil || !strings.Contains(err.Error(), "fixture binding insert failed") {
		t.Fatalf("expected insertion failure: %v", err)
	}
	c.bound(t, old)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_binding_history WHERE unbound_at IS NOT NULL`, 0)
	invitationSQL(t, db, `DROP TRIGGER reject_fixture_binding ON qq_activity_bindings`)
	if err = s.ProcessPrivate(context.Background(), e, nil); err != nil {
		t.Fatal(err)
	}
	c.bound(t, next)
	t.Log("failed final insert restored original relationship and history atomically; retry completed once")
}

func TestPostgresPrivateLateInviterCannotSelfClaim(t *testing.T) {
	db, s := privateFixture(t)
	user := autoAdd(t, db, "23456789@qq.com", "")
	if err := s.Process(context.Background(), invitationJoin("invited-unbound", "23456789", "12345678")); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards WHERE kind='newcomer' AND status='paid'`, 1)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards WHERE kind='invitation' AND recipient_user_id IS NULL`, 1)
	if err := s.Unbind(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	c := privateMember(t, db, s, "12345678")
	c.bind(t, "23456789@qq.com")
	c.bound(t, user)
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_rewards WHERE kind='invitation' AND status='paid'`, 0)
	autoCount(t, db, `SELECT COUNT(*) FROM user_affiliate_ledger`, 0)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM user_affiliate_ledger`, 0)
	t.Log("original newcomer account moved to previously unbound inviter; historical self-invitation could not claim")
}

func TestPostgresPrivateMigrationBackfillAndRepeat(t *testing.T) {
	db, _ := autoBindingFixtureAt(t, false)
	apply := func(name string) {
		t.Helper()
		data, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		invitationSQL(t, db, string(data))
	}
	apply("251_qq_activity_email_binding.sql")
	id := autoAdd(t, db, "old@example.test", "")
	invitationSQL(t, db, `INSERT INTO qq_activity_bindings(qq,user_id,source) VALUES('12345001',$1,'verified_email')`, id)
	invitationSQL(t, db, `INSERT INTO qq_activity_baseline_users(user_id,registered_at,email) VALUES($1,NOW(),'before-rename@example.test')`, id)
	invitationSQL(t, db, `DELETE FROM qq_activity_bindings`)
	paid := autoAdd(t, db, "reward-only@example.test", "")
	invitationSQL(t, db, `INSERT INTO qq_activity_rewards(kind,recipient_qq,recipient_user_id,subject_qq,subject_user_id,status,paid_at) VALUES('newcomer','12345002',$1,'12345002',$1,'paid',NOW())`, paid)
	// Legacy data can contain a replacement account whose only earlier proof
	// is a reward plus a retained mailbox. It must not get a first opportunity.
	recreated := autoAdd(t, db, "reward-only@example.test", "")
	invitationSQL(t, db, `INSERT INTO qq_activity_bindings(qq,user_id,source) VALUES('12345003',$1,'verified_email')`, recreated)
	apply("254_qq_activity_private_binding.sql")
	apply("254_qq_activity_private_binding.sql")
	for _, identity := range []struct{ kind, key string }{{"qq", "12345001"}, {"user", fmt.Sprint(id)}, {"email", Hash("old@example.test")}, {"email", Hash("before-rename@example.test")}, {"qq", "12345002"}, {"user", fmt.Sprint(paid)}, {"email", Hash("reward-only@example.test")}} {
		autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_ever_bound WHERE identity_kind=$1 AND identity_key=$2`, 1, identity.kind, identity.key)
	}
	autoCount(t, db, `SELECT COUNT(*) FROM qq_activity_first_bindings`, 0)
	t.Log("unbound-but-never-paid, proven reward-only identities and historical baseline email backfilled; migration repeat preserved records")
}
