package qqactivity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func mailChallenge(t *testing.T, s *Store, userID int64, qq string) (EmailChallenge, string) {
	t.Helper()
	var code string
	v, err := s.SendBindingCode(context.Background(), userID, qq, "192.0.2.1", func(_ context.Context, email, c string) error {
		if !strings.HasSuffix(email, "@qq.com") || !codePattern.MatchString(c) {
			t.Error("invalid SMTP envelope")
		}
		code = c
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return v, code
}

func mailAge(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("UPDATE qq_activity_mail_codes SET created_at=created_at-INTERVAL '61 seconds'"); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresQQEmailBinding(t *testing.T) {
	ctx := context.Background()
	t.Run("other_email_needs_user_scoped_mail_proof", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		id := autoAdd(t, db, "member@example.test", "12345678")
		other := autoAdd(t, db, "other@example.test", "")
		autoInitialize(t, s, "12345678")
		v, code := mailChallenge(t, s, id, " 12345678@QQ.COM ")
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 0)
		if v.QQEmail != "12****78@qq.com" {
			t.Fatal("unmasked email response")
		}
		var hash string
		if err := db.QueryRow("SELECT code_hash FROM qq_activity_mail_codes WHERE id=$1", v.Challenge).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if hash == code || hash != Hash(v.Challenge+":"+code) {
			t.Fatal("code not scoped and hashed")
		}
		for _, attempt := range []struct {
			user      int64
			email     string
			challenge string
		}{{other, "12345678", v.Challenge}, {id, "23456789", v.Challenge}, {id, "12345678", strings.Repeat("x", 48)}} {
			if _, err := s.VerifyBindingEmail(ctx, attempt.user, attempt.email, attempt.challenge, code); !errors.Is(err, ErrBindingCode) {
				t.Fatalf("foreign proof accepted: %v", err)
			}
		}
		status, err := s.VerifyBindingEmail(ctx, id, "12345678", v.Challenge, code)
		if err != nil || !status.Bound {
			t.Fatalf("valid proof failed: %v", err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE source='verified_email'", 1)
		autoCount(t, db, "SELECT count(*) FROM users WHERE id=$1 AND email='member@example.test' AND username='12345678'", 1, id)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards", 0) // existing user
		status, err = s.BindingStatus(ctx, id)
		if err != nil || !status.Bound || status.QQEmail != "12****78@qq.com" {
			t.Fatal("header status incorrect")
		}
		var text string
		if err = db.QueryRow("SELECT text FROM qq_activity_outbox WHERE dedup_key='binding:mail:12345678'").Scan(&text); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "member@example.test") || strings.Contains(text, "$2") {
			t.Fatalf("unsafe ordinary binding notice: %s", text)
		}
		if _, err = s.VerifyBindingEmail(ctx, id, "12345678", v.Challenge, code); err != nil {
			t.Fatal("verification retry must be idempotent", err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_binding_history", 1)
	})
	t.Run("wrong_codes_expire_after_five_committed_attempts", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		id := autoAdd(t, db, "member@example.test", "")
		v, code := mailChallenge(t, s, id, "12345678")
		wrong := "000000"
		if code == wrong {
			wrong = "111111"
		}
		for i := 0; i < 5; i++ {
			if _, err := s.VerifyBindingEmail(ctx, id, "12345678", v.Challenge, wrong); !errors.Is(err, ErrBindingCode) {
				t.Fatal(err)
			}
		}
		autoCount(t, db, "SELECT attempts FROM qq_activity_mail_codes WHERE id=$1", 5, v.Challenge)
		if _, err := s.VerifyBindingEmail(ctx, id, "12345678", v.Challenge, code); !errors.Is(err, ErrBindingCode) {
			t.Fatal("exhausted challenge accepted")
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 0)
		mailAge(t, db)
		fresh, freshCode := mailChallenge(t, s, id, "12345678")
		if _, err := s.VerifyBindingEmail(ctx, id, "12345678", v.Challenge, code); !errors.Is(err, ErrBindingCode) {
			t.Fatal("old challenge survived resend")
		}
		if _, err := db.Exec("UPDATE qq_activity_mail_codes SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1", fresh.Challenge); err != nil {
			t.Fatal(err)
		}
		if _, err := s.VerifyBindingEmail(ctx, id, "12345678", fresh.Challenge, freshCode); !errors.Is(err, ErrBindingCode) {
			t.Fatal("expired challenge accepted")
		}
	})
	t.Run("smtp_failure_and_cooldowns_fail_closed", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		id := autoAdd(t, db, "member@example.test", "")
		called := 0
		_, err := s.SendBindingCode(ctx, id, "12345678", "192.0.2.1", func(context.Context, string, string) error { called++; return errors.New("fixture SMTP failure") })
		if !errors.Is(err, ErrUnavailable) || called != 1 {
			t.Fatal("SMTP error not handled")
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_mail_codes WHERE sent_at IS NULL AND invalidated_at IS NOT NULL", 1)
		_, err = s.SendBindingCode(ctx, id, "23456789", "192.0.2.1", func(context.Context, string, string) error { called++; return nil })
		if !errors.Is(err, ErrBindingRate) || called != 1 {
			t.Fatal("user cooldown did not prevent second email")
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 0)
		mailAge(t, db)
		for i := 0; i < 4; i++ {
			mailChallenge(t, s, id, "12345678")
			mailAge(t, db)
		}
		_, err = s.SendBindingCode(ctx, id, "12345678", "192.0.2.1", func(context.Context, string, string) error { called++; return nil })
		if !errors.Is(err, ErrBindingRate) {
			t.Fatal("hourly send cap not enforced")
		}
	})
	t.Run("registration_and_historical_ownership_are_unique", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		owner := autoAdd(t, db, "12345678@qq.com", "")
		other := autoAdd(t, db, "other@example.test", "12345678")
		_, err := s.SendBindingCode(ctx, other, "12345678", "192.0.2.1", func(context.Context, string, string) error { t.Error("reserved identity emailed"); return nil })
		if !errors.Is(err, ErrBindingConflict) {
			t.Fatal("registration owner ignored")
		}
		autoInitialize(t, s, "12345678")
		if err = s.Unbind(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("UPDATE users SET email='changed@example.test',username='' WHERE id=$1", owner); err != nil {
			t.Fatal(err)
		}
		_, err = s.SendBindingCode(ctx, other, "12345678", "192.0.2.1", func(context.Context, string, string) error { t.Error("historical identity emailed"); return nil })
		if !errors.Is(err, ErrBindingConflict) {
			t.Fatal("unbind released historical ownership")
		}
	})
	t.Run("concurrent_verifications_and_registration_race", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		a := autoAdd(t, db, "a@example.test", "")
		b := autoAdd(t, db, "b@example.test", "")
		v1, c1 := mailChallenge(t, s, a, "12345678")
		mailAge(t, db)
		v2, c2 := mailChallenge(t, s, b, "12345678")
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, v := range []struct {
			id int64
			v  EmailChallenge
			c  string
		}{{a, v1, c1}, {b, v2, c2}} {
			wg.Add(1)
			go func(v struct {
				id int64
				v  EmailChallenge
				c  string
			}) {
				defer wg.Done()
				_, err := s.VerifyBindingEmail(ctx, v.id, "12345678", v.v.Challenge, v.c)
				errs <- err
			}(v)
		}
		wg.Wait()
		close(errs)
		success := 0
		for err := range errs {
			if err == nil {
				success++
			} else if !errors.Is(err, ErrBindingConflict) {
				t.Fatal(err)
			}
		}
		if success != 1 {
			t.Fatalf("concurrent ownership wins=%d", success)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 1)
		mailAge(t, db)
		c := autoAdd(t, db, "c@example.test", "")
		v3, c3 := mailChallenge(t, s, c, "23456789")
		autoAdd(t, db, "23456789@qq.com", "")
		if _, err := s.VerifyBindingEmail(ctx, c, "23456789", v3.Challenge, c3); !errors.Is(err, ErrBindingConflict) {
			t.Fatal("new registration owner lost")
		}
	})
	t.Run("unbind_stays_unbound_until_explicit_mail_verification", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		id := autoAdd(t, db, "12345678@qq.com", "")
		autoInitialize(t, s, "12345678")
		if err := s.Unbind(ctx, id); err != nil {
			t.Fatal(err)
		}
		if err := s.Unbind(ctx, id); err != nil {
			t.Fatal("unbind retry failed", err)
		}
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		level := 21
		for i, kind := range []string{"join", "command"} {
			if err := s.Process(ctx, Event{ID: []string{"rejoin", "query"}[i], GroupID: "87654321", QQ: "12345678", Kind: kind, Command: "绑定", Time: time.Now().Unix(), Level: &level, MemberConfirmed: true}); err != nil {
				t.Fatal(err)
			}
		}
		status, err := s.BindingStatus(ctx, id)
		if err != nil || status.Bound {
			t.Fatal("unbind was silently reversed")
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_binding_history WHERE unbound_at IS NOT NULL", 1)
		var prompt string
		if err = db.QueryRow("SELECT text FROM qq_activity_outbox WHERE dedup_key='event:query'").Scan(&prompt); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, "验证本人 QQ 邮箱") || strings.Contains(prompt, "修改用户名") {
			t.Fatalf("wrong unbound guidance: %s", prompt)
		}
		v, code := mailChallenge(t, s, id, "12345678")
		if _, err = s.VerifyBindingEmail(ctx, id, "12345678", v.Challenge, code); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_binding_history", 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards", 0)
	})
	t.Run("mail_binding_pays_newcomer_before_inviter_and_unbind_never_resets_awards", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		inviter := autoAdd(t, db, "12345678@qq.com", "")
		autoInitialize(t, s, "12345678")
		invitationSQL(t, db, "UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'")
		invitee := autoAdd(t, db, "invitee@example.test", "")
		if err := s.Process(ctx, invitationJoin("mail-join", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 0, 0)
		v, code := mailChallenge(t, s, invitee, "23456789")
		if _, err := s.VerifyBindingEmail(ctx, invitee, "23456789", v.Challenge, code); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 2, 2)
		if _, err := s.VerifyBindingEmail(ctx, invitee, "23456789", v.Challenge, code); err != nil {
			t.Fatal(err)
		}
		if err := s.Unbind(ctx, invitee); err != nil {
			t.Fatal(err)
		}
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='paid'", 2)
		mailAge(t, db)
		v, code = mailChallenge(t, s, invitee, "23456789")
		if _, err := s.VerifyBindingEmail(ctx, invitee, "23456789", v.Challenge, code); err != nil {
			t.Fatal(err)
		}
		if err := s.Process(ctx, invitationJoin("mail-rejoin", "23456789", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, "SELECT count(*) FROM user_affiliate_ledger", 1)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_outbox WHERE dedup_key LIKE 'reward:%'", 2)
		if err := s.Unbind(ctx, inviter); err != nil {
			t.Fatal(err)
		}
		mailAge(t, db)
		iv, ic := mailChallenge(t, s, inviter, "12345678")
		if _, err := s.VerifyBindingEmail(ctx, inviter, "12345678", iv.Challenge, ic); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, "SELECT count(*) FROM user_affiliate_ledger", 1)
		if err := s.Unbind(ctx, invitee); err != nil {
			t.Fatal(err)
		}
		mailAge(t, db)
		v, code = mailChallenge(t, s, invitee, "34567890")
		if _, err := s.VerifyBindingEmail(ctx, invitee, "34567890", v.Challenge, code); err != nil {
			t.Fatal(err)
		}
		if err := s.Process(ctx, invitationJoin("different-qq", "34567890", "12345678")); err != nil {
			t.Fatal(err)
		}
		invitationMoney(t, db, inviter, invitee, 2, 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards", 2)
	})
	t.Run("unbind_opt_out_survives_registration_email_change", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		id := autoAdd(t, db, "12345678@qq.com", "")
		autoInitialize(t, s, "12345678", "23456789")
		if err := s.Unbind(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE users SET email='23456789@qq.com' WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE user_id=$1", 0, id)
		v, code := mailChallenge(t, s, id, "23456789")
		if _, err := s.VerifyBindingEmail(ctx, id, "23456789", v.Challenge, code); err != nil {
			t.Fatal(err)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE user_id=$1", 1, id)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_identity_owners WHERE user_id=$1", 2, id)
	})
	t.Run("binding_lock_cannot_be_removed_by_self_service", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		id := autoAdd(t, db, "12345678@qq.com", "")
		autoInitialize(t, s, "12345678")
		if err := s.LockBinding(ctx, "fixture", "12345678", "fixture restriction"); err != nil {
			t.Fatal(err)
		}
		if err := s.Unbind(ctx, id); !errors.Is(err, ErrBindingConflict) {
			t.Fatal("locked binding removed")
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE status='locked'", 1)
	})
}
