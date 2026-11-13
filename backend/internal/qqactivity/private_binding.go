package qqactivity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"
)

// PrivateEvent's QQ comes exclusively from the authenticated OneBot bridge.
// Text is used in memory only; neither event rows nor audit rows contain it.
type PrivateEvent struct {
	ID              string `json:"id"`
	QQ              string `json:"qq"`
	Text            string `json:"text"`
	Time            int64  `json:"time"`
	GroupID         string `json:"group_id"`
	MemberConfirmed bool   `json:"member_confirmed"`
	Level           *int   `json:"level,omitempty"`
}

type privateSession struct {
	step, email, challenge, codeHash string
	target, original                 sql.NullInt64
	originalBound                    sql.NullTime
	attempts                         int
	sent                             sql.NullTime
	expires, updated                 time.Time
}

type privateMail struct{ qq, email, challenge, code string }

func recoverPrivateSessions(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM qq_activity_private_sessions WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `UPDATE qq_activity_private_sessions SET step='confirm_email',code_hash='',sent_at=NULL,updated_at=clock_timestamp() WHERE step='sending_code' AND updated_at<NOW()-INTERVAL '2 minutes' RETURNING qq,challenge`)
	if err != nil {
		return err
	}
	type recovery struct{ qq, challenge string }
	var recoveries []recovery
	for rows.Next() {
		var r recovery
		if err = rows.Scan(&r.qq, &r.challenge); err != nil {
			rows.Close()
			return err
		}
		recoveries = append(recoveries, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range recoveries {
		if err = enqueuePrivate(ctx, tx, "private-recover:"+r.challenge, r.qq, "验证码发送未完成，请回复【重发】重新获取。原有绑定保持不变。"); err != nil {
			return err
		}
	}
	return nil
}

func privateEmail(input string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(input))
	a, err := mail.ParseAddress(v)
	return v, err == nil && a.Address == v && len(v) <= 254 && emailPattern.FindString(v) == v
}

func enqueuePrivate(ctx context.Context, tx *sql.Tx, key, qq, text string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO qq_activity_outbox(dedup_key,group_id,qq,text,scope) VALUES($1,'',$2,$3,'private') ON CONFLICT(dedup_key) DO NOTHING`, key, qq, MaskText(text))
	return err
}

func (s *Store) FriendAdded(ctx context.Context, qq string) error {
	if !ValidQQ(qq) {
		return errors.New("invalid friend identity")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		return enqueuePrivate(ctx, tx, "friend:"+qq, qq, "你好，发送【绑定】开始绑定官网账号。")
	})
}

func savePrivateSession(ctx context.Context, tx *sql.Tx, qq string, p *privateSession) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO qq_activity_private_sessions(qq,step,target_user_id,target_email,original_user_id,original_bound_at,challenge,code_hash,attempts,sent_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(qq) DO UPDATE SET step=EXCLUDED.step,target_user_id=EXCLUDED.target_user_id,target_email=EXCLUDED.target_email,original_user_id=EXCLUDED.original_user_id,original_bound_at=EXCLUDED.original_bound_at,challenge=EXCLUDED.challenge,code_hash=EXCLUDED.code_hash,attempts=EXCLUDED.attempts,sent_at=EXCLUDED.sent_at,expires_at=EXCLUDED.expires_at,updated_at=clock_timestamp()`, qq, p.step, p.target, p.email, p.original, p.originalBound, p.challenge, p.codeHash, p.attempts, p.sent, p.expires)
	return err
}

func privateCurrent(ctx context.Context, tx *sql.Tx, qq string) (user sql.NullInt64, bound sql.NullTime, email, status string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT b.user_id,b.bound_at,COALESCE(u.email,''),b.status FROM qq_activity_bindings b LEFT JOIN users u ON u.id=b.user_id WHERE b.qq=$1 FOR UPDATE OF b`, qq).Scan(&user, &bound, &email, &status)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func privateTarget(ctx context.Context, tx *sql.Tx, qq string, p *privateSession) error {
	var email, status string
	err := tx.QueryRowContext(ctx, `SELECT email,status FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, p.target).Scan(&email, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBindingConflict
	}
	if err != nil {
		return err
	}
	if status != "active" || strings.ToLower(strings.TrimSpace(email)) != p.email {
		return ErrBindingConflict
	}
	var conflict bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_bindings WHERE user_id=$1 AND qq<>$2)`, p.target, qq).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return ErrBindingConflict
	}
	return nil
}

// SMTP is outside the global activity transaction. A crash/uncertain send
// leaves an unusable sending challenge; the member can resend after cooldown.
// Only hashes survive, and a stale SMTP completion cannot revive a canceled
// session. Login, registration and website QQ OTPs use other tables/purposes.
func (s *Store) ProcessPrivate(ctx context.Context, e PrivateEvent, send func(context.Context, string, string) error) error {
	if !ValidQQ(e.QQ) || len(e.ID) == 0 || len(e.ID) > 160 || len(e.Text) > 512 || e.Time <= 0 || e.Time > time.Now().Add(5*time.Minute).Unix() {
		return errors.New("invalid private event")
	}
	var job *privateMail
	var changed []int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO qq_activity_private_events(event_id,qq,occurred_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, e.ID, e.QQ, time.Unix(e.Time, 0))
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		reply := func(text string) error { return enqueuePrivate(ctx, tx, "private:"+e.ID, e.QQ, text) }
		clear := func() error {
			_, err := tx.ExecContext(ctx, `DELETE FROM qq_activity_private_sessions WHERE qq=$1`, e.QQ)
			return err
		}
		text := strings.TrimSpace(e.Text)
		if text == "取消" {
			if err = clear(); err != nil {
				return err
			}
			return reply("已取消本次操作，原有绑定保持不变。发送【绑定】可重新开始。")
		}
		var groupOK bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_groups WHERE group_id=$1 AND bot_qq<>$2)`, e.GroupID, e.QQ).Scan(&groupOK); err != nil {
			return err
		}
		if !groupOK || !e.MemberConfirmed {
			return reply("请先加入活动群，再私信发送【绑定】。当前未确认你的群成员身份，尚未更改绑定。")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_members(group_id,qq,legacy,first_joined_at) VALUES($1,$2,TRUE,NOW()) ON CONFLICT(group_id,qq) DO UPDATE SET in_group=TRUE,last_seen_at=NOW()`, e.GroupID, e.QQ); err != nil {
			return err
		}
		if e.Level != nil && *e.Level > 0 && *e.Level <= 1000 {
			if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_members SET qq_level=$1,level_checked_at=NOW(),next_level_retry_at=NULL WHERE qq=$2 AND group_id=$3`, e.Level, e.QQ, e.GroupID); err != nil {
				return err
			}
		}
		var robot bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(BOOL_OR(is_robot),FALSE) FROM qq_activity_members WHERE qq=$1`, e.QQ).Scan(&robot); err != nil {
			return err
		}
		if robot {
			return nil
		}
		current, boundAt, currentEmail, status, err := privateCurrent(ctx, tx, e.QQ)
		if err != nil {
			return err
		}
		if status == "locked" {
			if err = clear(); err != nil {
				return err
			}
			return reply("当前绑定已被管理员锁定，请联系管理员核验，原有绑定保持不变。")
		}
		var p privateSession
		err = tx.QueryRowContext(ctx, `SELECT step,target_user_id,target_email,original_user_id,original_bound_at,challenge,code_hash,attempts,sent_at,expires_at,updated_at FROM qq_activity_private_sessions WHERE qq=$1 FOR UPDATE`, e.QQ).Scan(&p.step, &p.target, &p.email, &p.original, &p.originalBound, &p.challenge, &p.codeHash, &p.attempts, &p.sent, &p.expires, &p.updated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (current != p.original || boundAt != p.originalBound) {
			if err = clear(); err != nil {
				return err
			}
			return reply("绑定状态已发生变化，本次操作已取消。请重新发送【绑定】。")
		}
		_, emailInput := privateEmail(text)
		continuing := text == "确认" || text == "确认绑定" || text == "重发" || text == "更换" || text == "确认更换" || text == "是" || codePattern.MatchString(text)
		if (errors.Is(err, sql.ErrNoRows) && continuing) || (err == nil && !p.expires.After(time.Now()) && text != "绑定" && text != "更换绑定" && !emailInput) {
			if err = clear(); err != nil {
				return err
			}
			return reply("本次绑定操作已过期或结束。请重新发送官网注册邮箱，或发送【绑定】重新开始。原有绑定保持不变。")
		}
		if errors.Is(err, sql.ErrNoRows) || !p.expires.After(time.Now()) || text == "绑定" || text == "更换绑定" {
			p = privateSession{step: "email", original: current, originalBound: boundAt, expires: time.Now().Add(10 * time.Minute)}
			if current.Valid {
				p.step = "confirm_change"
				if err = savePrivateSession(ctx, tx, e.QQ, &p); err != nil {
					return err
				}
				return reply("你已绑定官网账号：" + MaskEmail(currentEmail) + "。是否更换？回复【更换】继续，或回复【取消】。换绑不会重置新人或邀请奖励资格。")
			}
		}
		if p.step == "confirm_change" {
			if text != "更换" && text != "确认更换" && text != "是" {
				return reply("如需更换，请回复【更换】；保留当前绑定请回复【取消】。")
			}
			p.step = "email"
			p.expires = time.Now().Add(10 * time.Minute)
			if err = savePrivateSession(ctx, tx, e.QQ, &p); err != nil {
				return err
			}
			return reply("请在 10 分钟内发送要绑定的官网账号完整注册邮箱。支持自定义 QQ 邮箱、Gmail、163 等；原有绑定在全部确认完成前继续有效。")
		}
		if p.step == "email" || p.step == "confirm_email" {
			if email, ok := privateEmail(text); ok {
				var id int64
				rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE deleted_at IS NULL AND status='active' AND lower(btrim(email))=$1 ORDER BY id LIMIT 2`, email)
				if err != nil {
					return err
				}
				count := 0
				for rows.Next() {
					if err = rows.Scan(&id); err != nil {
						rows.Close()
						return err
					}
					count++
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if count != 1 {
					return reply("暂时无法使用该邮箱，请核对官网账号的注册邮箱及账号状态后重试。尚未注册的，请先到官网注册。")
				}
				if current.Valid && current.Int64 == id {
					if err = clear(); err != nil {
						return err
					}
					return reply("当前已绑定该官网账号，无需重复验证。")
				}
				p.target, p.email, p.step = sql.NullInt64{Int64: id, Valid: true}, email, "confirm_email"
				p.expires = time.Now().Add(10 * time.Minute)
				if err = privateTarget(ctx, tx, e.QQ, &p); err != nil {
					if errors.Is(err, ErrBindingConflict) {
						return reply("该官网账号当前无法绑定或已关联其他 QQ，请先由原绑定方解除关联，或联系管理员。")
					}
					return err
				}
				if err = savePrivateSession(ctx, tx, e.QQ, &p); err != nil {
					return err
				}
				return reply("是否绑定官网账号：" + MaskEmail(email) + "？请在 10 分钟内回复【确认】发送邮箱验证码，或回复【取消】。验证码将发送到该官网账号的注册邮箱，收到后只在本私信中填写。")
			}
			if p.step == "email" {
				p.expires = time.Now().Add(10 * time.Minute)
				if err = savePrivateSession(ctx, tx, e.QQ, &p); err != nil {
					return err
				}
				return reply("请在 10 分钟内发送要绑定的官网账号完整注册邮箱，支持任意邮箱。QQ 身份以当前私信账号为准，无需修改官网账号资料。回复【取消】退出。")
			}
		}
		if p.step == "confirm_email" || p.step == "sending_code" || (p.step == "verify_code" && text == "重发") {
			if text != "确认" && text != "确认绑定" && text != "重发" {
				return reply("回复【确认】发送验证码，回复【重发】重新发送，或回复【取消】。")
			}
			if send == nil {
				return reply("邮件发送暂时不可用，请稍后回复【重发】。原有绑定保持不变。")
			}
			if err = privateTarget(ctx, tx, e.QQ, &p); err != nil {
				if errors.Is(err, ErrBindingConflict) {
					if err = clear(); err != nil {
						return err
					}
					return reply("目标账号状态已变化，本次操作已取消，请重新发送【绑定】。")
				}
				return err
			}
			var cooling bool
			var byQQ, byUser int
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_private_mail_sends WHERE (qq=$1 OR user_id=$2) AND created_at>NOW()-INTERVAL '60 seconds'),COUNT(*) FILTER(WHERE qq=$1),COUNT(*) FILTER(WHERE user_id=$2) FROM qq_activity_private_mail_sends WHERE created_at>NOW()-INTERVAL '1 hour'`, e.QQ, p.target).Scan(&cooling, &byQQ, &byUser); err != nil {
				return err
			}
			if cooling || byQQ >= 5 || byUser >= 5 {
				return reply(ErrBindingRate.Error())
			}
			challenge, err := randomToken()
			if err != nil {
				return err
			}
			n, err := rand.Int(rand.Reader, big.NewInt(1000000))
			if err != nil {
				return err
			}
			code := fmt.Sprintf("%06d", n.Int64())
			p.challenge, p.codeHash, p.attempts, p.sent, p.step = challenge, Hash("qq-private:"+challenge+":"+code), 0, sql.NullTime{}, "sending_code"
			p.expires = time.Now().Add(10 * time.Minute)
			if err = savePrivateSession(ctx, tx, e.QQ, &p); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_private_mail_sends(challenge,qq,user_id) VALUES($1,$2,$3)`, challenge, e.QQ, p.target); err != nil {
				return err
			}
			job = &privateMail{e.QQ, p.email, challenge, code}
			return nil
		}
		if p.step == "verify_code" {
			if !p.sent.Valid || p.attempts >= 5 {
				return reply("验证码已失效，请回复【重发】获取新验证码。")
			}
			if !codePattern.MatchString(text) || subtle.ConstantTimeCompare([]byte(p.codeHash), []byte(Hash("qq-private:"+p.challenge+":"+text))) != 1 {
				p.attempts++
				if p.attempts >= 5 {
					p.codeHash = ""
				}
				if err = savePrivateSession(ctx, tx, e.QQ, &p); err != nil {
					return err
				}
				return reply(fmt.Sprintf("验证码不正确，还可尝试 %d 次。回复【重发】获取新验证码，或回复【取消】。", max(0, 5-p.attempts)))
			}
			p.codeHash = ""
			if current.Valid {
				p.step = "confirm_switch"
				if err = savePrivateSession(ctx, tx, e.QQ, &p); err != nil {
					return err
				}
				return reply("邮箱验证通过。将从 " + MaskEmail(currentEmail) + " 更换为 " + MaskEmail(p.email) + "。回复【确认更换】完成，或回复【取消】保留原绑定。已有余额和已确认奖励仍归原官网账号，换绑不重置领奖资格。")
			}
		} else if p.step == "confirm_switch" {
			if text != "确认更换" {
				return reply("回复【确认更换】完成更换，或回复【取消】保留原绑定。")
			}
		} else {
			return reply("请按上一步提示操作，或发送【绑定】重新开始。")
		}
		if err = privateTarget(ctx, tx, e.QQ, &p); err != nil {
			if errors.Is(err, ErrBindingConflict) {
				if err = clear(); err != nil {
					return err
				}
				return reply("目标账号已变化或被其他 QQ 绑定，本次操作已取消，原有绑定保持不变。")
			}
			return err
		}
		if current.Valid {
			if _, err = tx.ExecContext(ctx, `DELETE FROM qq_activity_bindings WHERE qq=$1`, e.QQ); err != nil {
				return err
			}
		}
		if err = bind(ctx, tx, e.QQ, p.target.Int64, "verified_private_email"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_mail_codes SET invalidated_at=NOW() WHERE (qq=$1 OR user_id=$2 OR user_id=$3) AND consumed_at IS NULL AND invalidated_at IS NULL`, e.QQ, current, p.target); err != nil {
			return err
		}
		if err = clear(); err != nil {
			return err
		}
		if err = audit(ctx, tx, "qq:"+e.QQ, "private_binding", fmt.Sprint(p.target.Int64), map[string]any{"previous_user_id": current, "history_retained": true}); err != nil {
			return err
		}
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		if err = s.reconcile(ctx, tx, e.QQ, c); err != nil {
			return err
		}
		changed, err = s.settle(ctx, tx, c)
		if err != nil {
			return err
		}
		verb := "绑定成功"
		if current.Valid {
			verb = "更换绑定成功"
		}
		return reply(verb + "！已关联官网账号：" + MaskEmail(p.email) + "。奖励以资格核验和实际到账记录为准；曾绑定过的 QQ 或官网账号不会重新获得新人资格。")
	})
	if err != nil {
		return err
	}
	s.invalidateUsers(changed)
	if job == nil {
		return nil
	}
	sendErr := send(ctx, job.email, job.code)
	finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.transaction(finish, func(tx *sql.Tx) error {
		step, message := "verify_code", "验证码已发送至 "+MaskEmail(job.email)+"，10 分钟内有效。请在本私信发送 6 位验证码，不要发到群里。回复【重发】可重新发送，或回复【取消】。"
		if sendErr != nil {
			step, message = "confirm_email", "验证码发送未完成，请稍后回复【重发】。原有绑定保持不变。"
		}
		result, err := tx.ExecContext(finish, `UPDATE qq_activity_private_sessions SET step=$1,sent_at=CASE WHEN $1='verify_code' THEN clock_timestamp() END,code_hash=CASE WHEN $1='verify_code' THEN code_hash ELSE '' END,updated_at=clock_timestamp() WHERE qq=$2 AND challenge=$3 AND step='sending_code'`, step, job.qq, job.challenge)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return enqueuePrivate(finish, tx, "private-mail:"+job.challenge, job.qq, message)
	})
}
