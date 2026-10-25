package qqactivity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var (
	ErrBindingEmail    = errors.New("请输入本人 QQ 号或完整数字 QQ 邮箱，例如 12345678@qq.com。")
	ErrBindingConflict = errors.New("该 QQ 或官网账号已有关联，请核对账号或联系管理员。")
	ErrBindingCode     = errors.New("验证码无效或已过期，请重新获取后重试。")
	ErrBindingRate     = errors.New("验证码发送较频繁，请稍后再试。")
	ErrBindingAlready  = errors.New("你已经绑定 QQ，无需重复绑定。")
)

type BindingStatus struct {
	Bound   bool   `json:"bound"`
	QQEmail string `json:"qq_email,omitempty"`
}

type EmailChallenge struct {
	Challenge   string    `json:"challenge"`
	QQEmail     string    `json:"qq_email"`
	ExpiresAt   time.Time `json:"expires_at"`
	ResendAfter int       `json:"resend_after"`
}

func normalizeQQEmail(input string) (qq, email string, err error) {
	email = strings.ToLower(strings.TrimSpace(input))
	if ValidQQ(email) {
		email += "@qq.com"
	}
	if !strings.HasSuffix(email, "@qq.com") {
		return "", "", ErrBindingEmail
	}
	qq = strings.TrimSuffix(email, "@qq.com")
	if !ValidQQ(qq) {
		return "", "", ErrBindingEmail
	}
	return qq, email, nil
}

func (s *Store) BindingStatus(ctx context.Context, userID int64) (BindingStatus, error) {
	var qq string
	err := s.db.QueryRowContext(ctx, `SELECT qq FROM qq_activity_bindings WHERE user_id=$1`, userID).Scan(&qq)
	if errors.Is(err, sql.ErrNoRows) {
		return BindingStatus{}, nil
	}
	if err != nil {
		return BindingStatus{}, err
	}
	return BindingStatus{Bound: true, QQEmail: MaskEmail(qq + "@qq.com")}, nil
}

func (s *Store) Unbind(ctx context.Context, userID int64) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var qq, status string
		err := tx.QueryRowContext(ctx, `SELECT qq,status FROM qq_activity_bindings WHERE user_id=$1 FOR UPDATE`, userID).Scan(&qq, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != "active" {
			return ErrBindingConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM qq_activity_bindings WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_members SET candidate_user_id=NULL,match_source='',match_state='unbound' WHERE qq=$1`, qq); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_mail_codes SET invalidated_at=NOW() WHERE user_id=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, userID); err != nil {
			return err
		}
		return audit(ctx, tx, fmt.Sprintf("user:%d", userID), "unbind", qq, map[string]bool{"history_retained": true, "rewards_retained": true})
	})
}

// Registration email and permanent bindings arbitrate ownership. Unverified
// profile aliases neither select an account nor reserve a mailbox anymore.
func bindingTarget(ctx context.Context, tx *sql.Tx, userID int64, qq string) error {
	var email, status string
	if err := tx.QueryRowContext(ctx, `SELECT email,status FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&email, &status); err != nil {
		return ErrBindingConflict
	}
	if status != "active" {
		return ErrBindingConflict
	}
	registered, _, err := normalizeQQEmail(email)
	if err == nil && registered != qq {
		return ErrBindingConflict
	}
	var conflict bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_identity_owners WHERE qq=$1 AND user_id<>$2) OR EXISTS(SELECT 1 FROM qq_activity_bindings WHERE (qq=$1 AND user_id<>$2) OR (user_id=$2 AND qq<>$1))`, qq, userID).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return ErrBindingConflict
	}
	return nil
}

// Codes are scoped to this purpose, authenticated user, QQ and random challenge.
// No registration/login/notification-email code can be reused here. Reserve
// cooldowns transactionally, then send SMTP outside the activity lock.
func (s *Store) SendBindingCode(ctx context.Context, userID int64, input, ip string, send func(context.Context, string, string) error) (view EmailChallenge, err error) {
	qq, email, err := normalizeQQEmail(input)
	if err != nil {
		return view, err
	}
	if send == nil {
		return view, ErrUnavailable
	}
	id, err := randomToken()
	if err != nil {
		return view, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return view, err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := bindingTarget(ctx, tx, userID, qq); err != nil {
			return err
		}
		var bound bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_bindings WHERE user_id=$1)`, userID).Scan(&bound); err != nil {
			return err
		}
		if bound {
			return ErrBindingAlready
		}
		var cooldown bool
		var byUser, byQQ, byIP int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_mail_codes WHERE (user_id=$1 OR qq=$2) AND created_at>NOW()-INTERVAL '60 seconds'),COUNT(*) FILTER(WHERE user_id=$1),COUNT(*) FILTER(WHERE qq=$2),COUNT(*) FILTER(WHERE ip_hash=$3) FROM qq_activity_mail_codes WHERE created_at>NOW()-INTERVAL '1 hour'`, userID, qq, Hash(ip)).Scan(&cooldown, &byUser, &byQQ, &byIP); err != nil {
			return err
		}
		if cooldown || byUser >= 5 || byQQ >= 5 || byIP >= 20 {
			return ErrBindingRate
		}
		if _, err := tx.ExecContext(ctx, `UPDATE qq_activity_mail_codes SET invalidated_at=NOW() WHERE user_id=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, userID); err != nil {
			return err
		}
		view = EmailChallenge{Challenge: id, QQEmail: MaskEmail(email), ExpiresAt: time.Now().Add(10 * time.Minute), ResendAfter: 60}
		_, err := tx.ExecContext(ctx, `INSERT INTO qq_activity_mail_codes(id,user_id,qq,code_hash,ip_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, id, userID, qq, Hash(id+":"+code), Hash(ip), view.ExpiresAt)
		return err
	})
	if err != nil {
		return EmailChallenge{}, err
	}
	if err = send(ctx, email, code); err != nil {
		// Never leave a failed/uncertain send usable. Use an independent bounded
		// context since SMTP may have failed because the HTTP request was canceled.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.db.ExecContext(cleanup, `UPDATE qq_activity_mail_codes SET invalidated_at=NOW() WHERE id=$1`, id)
		return EmailChallenge{}, ErrUnavailable
	}
	_, err = s.db.ExecContext(ctx, `UPDATE qq_activity_mail_codes SET sent_at=NOW() WHERE id=$1 AND invalidated_at IS NULL`, id)
	if err != nil {
		return EmailChallenge{}, ErrUnavailable
	}
	return view, nil
}

func (s *Store) VerifyBindingEmail(ctx context.Context, userID int64, input, challenge, code string) (view BindingStatus, err error) {
	qq, _, err := normalizeQQEmail(input)
	if err != nil {
		return view, err
	}
	if len(challenge) != 48 {
		return view, ErrBindingCode
	}
	var rejected error
	var changed []int64
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var expected string
		var expires time.Time
		var sent, consumed, invalid sql.NullTime
		var attempts int
		if err := tx.QueryRowContext(ctx, `SELECT code_hash,expires_at,sent_at,consumed_at,invalidated_at,attempts FROM qq_activity_mail_codes WHERE id=$1 AND user_id=$2 AND qq=$3 FOR UPDATE`, challenge, userID, qq).Scan(&expected, &expires, &sent, &consumed, &invalid, &attempts); errors.Is(err, sql.ErrNoRows) {
			return ErrBindingCode
		} else if err != nil {
			return err
		}
		if consumed.Valid {
			var owned bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_bindings WHERE qq=$1 AND user_id=$2)`, qq, userID).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return ErrBindingCode
			}
			view = BindingStatus{Bound: true, QQEmail: MaskEmail(qq + "@qq.com")}
			return nil
		}
		if !sent.Valid || invalid.Valid || !expires.After(time.Now()) || attempts >= 5 {
			return ErrBindingCode
		}
		if !codePattern.MatchString(code) || subtle.ConstantTimeCompare([]byte(expected), []byte(Hash(challenge+":"+code))) != 1 {
			_, err := tx.ExecContext(ctx, `UPDATE qq_activity_mail_codes SET attempts=attempts+1,invalidated_at=CASE WHEN attempts+1>=5 THEN NOW() ELSE invalidated_at END WHERE id=$1`, challenge)
			rejected = ErrBindingCode
			return err // commit the failed attempt
		}
		if err := bindingTarget(ctx, tx, userID, qq); err != nil {
			return err
		}
		if err := bind(ctx, tx, qq, userID, "verified_email"); err != nil {
			return ErrBindingConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE qq_activity_mail_codes SET consumed_at=NOW(),code_hash='' WHERE id=$1`, challenge); err != nil {
			return err
		}
		if err := audit(ctx, tx, fmt.Sprintf("user:%d", userID), "verify_qq_email", qq, map[string]string{"source": "verified_email"}); err != nil {
			return err
		}
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		if err = s.reconcile(ctx, tx, qq, c); err != nil {
			return err
		}
		changed, err = s.settle(ctx, tx, c)
		if err != nil {
			return err
		}
		var group string
		err = tx.QueryRowContext(ctx, `SELECT group_id FROM qq_activity_members WHERE qq=$1 AND in_group AND NOT is_robot ORDER BY last_seen_at DESC LIMIT 1`, qq).Scan(&group)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if group != "" {
			summary, err := summaryForQQ(ctx, tx, qq, c)
			if err != nil {
				return err
			}
			if err = enqueue(ctx, tx, "binding:mail:"+qq, group, qq, renderBinding(summary, false)); err != nil {
				return err
			}
		}
		view = BindingStatus{Bound: true, QQEmail: MaskEmail(qq + "@qq.com")}
		return nil
	})
	if err == nil {
		s.invalidateUsers(changed)
		err = rejected
	}
	return view, err
}
