package qqactivity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"
)

type ClaimView struct {
	Account   string    `json:"account"`
	QQEmail   string    `json:"qq_email"`
	ExpiresAt time.Time `json:"expires_at"`
	Code      string    `json:"code,omitempty"`
	Consumed  bool      `json:"consumed"`
}

func createClaim(ctx context.Context, tx *sql.Tx, group, qq string, c *Candidate) (string, error) {
	var blocked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_claims WHERE qq=$1 AND attempts>=5 AND created_at>NOW()-INTERVAL '10 minutes')`, qq).Scan(&blocked); err != nil {
		return "", err
	}
	if blocked {
		return "确认尝试次数较多，请 10 分钟后重新发送「绑定」。", nil
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_claims SET invalidated_at=NOW() WHERE qq=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, qq); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_claims(token_hash,group_id,qq,candidate_user_id,expires_at) VALUES($1,$2,$3,$4,NOW()+INTERVAL '10 minutes')`, Hash(token), group, qq, c.ID); err != nil {
		return "", err
	}
	id := "M02"
	if c.Source == "email" {
		id = "M03"
	}
	return Prompt(id, map[string]string{"bind_url": SiteURL + "/qq-activity?claim=" + token, "account_mask": MaskEmail(c.Email)}), nil
}

// Claim is only readable by the already selected website account. The public
// link alone never changes the QQ identity or selects a different candidate.
func (s *Store) Claim(ctx context.Context, userID int64, token string, issueCode bool) (view ClaimView, err error) {
	if len(token) != 48 {
		return view, ErrClaim
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var qq, codeHash string
		var expires time.Time
		var consumed, invalid sql.NullTime
		var candidate int64
		var attempts int
		row := tx.QueryRowContext(ctx, `SELECT qq,candidate_user_id,expires_at,consumed_at,invalidated_at,COALESCE(code_hash,''),attempts FROM qq_activity_claims WHERE token_hash=$1 FOR UPDATE`, Hash(token))
		if err := row.Scan(&qq, &candidate, &expires, &consumed, &invalid, &codeHash, &attempts); err != nil {
			return ErrClaim
		}
		if candidate != userID || invalid.Valid || !expires.After(time.Now()) || attempts >= 5 {
			return ErrClaim
		}
		var email, status string
		if err := tx.QueryRowContext(ctx, `SELECT email,status FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&email, &status); err != nil || status != "active" {
			return ErrClaim
		}
		view = ClaimView{Account: MaskEmail(email), QQEmail: MaskEmail(qq + "@qq.com"), ExpiresAt: expires, Consumed: consumed.Valid}
		if issueCode && !consumed.Valid {
			n, err := rand.Int(rand.Reader, big.NewInt(1000000))
			if err != nil {
				return err
			}
			view.Code = fmt.Sprintf("%06d", n.Int64())
			_, err = tx.ExecContext(ctx, `UPDATE qq_activity_claims SET code_hash=$1 WHERE token_hash=$2`, Hash(Hash(token)+":"+view.Code), Hash(token))
			if err != nil {
				return err
			}
		}
		return nil
	})
	return
}

func confirmClaim(ctx context.Context, tx *sql.Tx, group, qq, code string) (string, error) {
	if !codePattern.MatchString(code) {
		return Prompt("M08", nil), nil
	}
	var tokenHash, expected string
	var userID int64
	var expires time.Time
	var consumed sql.NullTime
	var attempts int
	err := tx.QueryRowContext(ctx, `SELECT token_hash,candidate_user_id,expires_at,consumed_at,COALESCE(code_hash,''),attempts FROM qq_activity_claims WHERE qq=$1 AND group_id=$2 AND invalidated_at IS NULL ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, qq, group).Scan(&tokenHash, &userID, &expires, &consumed, &expected, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return Prompt("M09", nil), nil
	}
	if err != nil {
		return "", err
	}
	if consumed.Valid {
		return "", nil
	}
	if attempts >= 5 {
		return Prompt("M12", nil), nil
	}
	if expires.Before(time.Now()) {
		return Prompt("M10", nil), nil
	}
	actual := Hash(tokenHash + ":" + code)
	if expected == "" || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		_, err = tx.ExecContext(ctx, `UPDATE qq_activity_claims SET attempts=attempts+1 WHERE token_hash=$1`, tokenHash)
		if err != nil {
			return "", err
		}
		if attempts+1 >= 5 {
			return Prompt("M12", nil), nil
		}
		return Prompt("M09", nil), nil
	}
	var otherQQ string
	err = tx.QueryRowContext(ctx, `SELECT qq FROM qq_activity_bindings WHERE user_id=$1`, userID).Scan(&otherQQ)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && otherQQ != qq {
		return Prompt("M06", map[string]string{"case_no": Hash(group + ":" + qq)[:12]}), nil
	}
	if err = bind(ctx, tx, qq, userID, "dual_confirmation"); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_claims SET consumed_at=NOW() WHERE token_hash=$1`, tokenHash); err != nil {
		return "", err
	}
	return "", audit(ctx, tx, "qq:"+qq, "bind", fmt.Sprint(userID), map[string]any{"source": "dual_confirmation"})
}
