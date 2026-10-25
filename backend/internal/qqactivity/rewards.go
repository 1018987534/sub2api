package qqactivity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Alias r is an invitation reward. Both identities must match an actually
// credited newcomer reward; eligibility or a pending row is not payment proof.
const invitationNewcomerPaid = `EXISTS(SELECT 1 FROM qq_activity_rewards n WHERE n.kind='newcomer' AND n.status='paid' AND n.subject_qq=r.subject_qq AND n.subject_user_id=r.subject_user_id)`

func memberState(ctx context.Context, q queryer, qq string) (level *int, legacy, robot, inGroup bool, joined time.Time, err error) {
	err = q.QueryRowContext(ctx, `SELECT COALESCE(BOOL_OR(legacy),FALSE),COALESCE(BOOL_OR(is_robot),FALSE),COALESCE(BOOL_OR(in_group),FALSE),COALESCE(MIN(first_joined_at),NOW()) FROM qq_activity_members WHERE qq=$1`, qq).Scan(&legacy, &robot, &inGroup, &joined)
	if err != nil {
		return
	}
	var n sql.NullInt64
	e := q.QueryRowContext(ctx, `SELECT qq_level FROM qq_activity_members WHERE qq=$1 ORDER BY level_checked_at DESC NULLS LAST LIMIT 1`, qq).Scan(&n)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		err = e
		return
	}
	if n.Valid && n.Int64 > 0 {
		v := int(n.Int64)
		level = &v
	}
	return
}

func (s *Store) reconcile(ctx context.Context, tx *sql.Tx, qq string, c Config) error {
	var userID int64
	var status string
	var registered time.Time
	var baseline bool
	err := tx.QueryRowContext(ctx, `SELECT b.user_id,u.status,u.created_at,EXISTS(SELECT 1 FROM qq_activity_baseline_users x WHERE x.user_id=u.id OR lower(btrim(x.email))=lower(btrim(u.email))) FROM qq_activity_bindings b JOIN users u ON u.id=b.user_id AND u.deleted_at IS NULL WHERE b.qq=$1 AND b.status='active'`, qq).Scan(&userID, &status, &registered, &baseline)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return nil
	}
	level, legacy, robot, inGroup, joined, err := memberState(ctx, tx, qq)
	if err != nil {
		return err
	}
	canQualify := WithinCampaign(c, time.Now()) && (c.Status == "active" || c.Status == "paused")
	if canQualify && inGroup && NewcomerQualifies(baseline || legacy, c.StartedAt != nil && joined.After(*c.StartedAt), c.StartedAt != nil && registered.After(*c.StartedAt), level, robot) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_rewards(kind,recipient_qq,recipient_user_id,subject_qq,subject_user_id) VALUES('newcomer',$1,$2,$1,$2) ON CONFLICT DO NOTHING`, qq, userID); err != nil {
			return err
		}
	}
	var inviter, evidenceType, evidenceKey string
	var qualified sql.NullTime
	var inviteJoined time.Time
	err = tx.QueryRowContext(ctx, `SELECT inviter_qq,evidence_type,evidence_key,qualified_at,joined_at FROM qq_activity_invitations WHERE invitee_qq=$1 FOR UPDATE`, qq).Scan(&inviter, &evidenceType, &evidenceKey, &qualified, &inviteJoined)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	evidence := ValidQQ(inviter) && evidenceKey != "" && (evidenceType == "direct_invite_pinned" || evidenceType == "admin_verified")
	var inviterUser sql.NullInt64
	if ValidQQ(inviter) {
		e := tx.QueryRowContext(ctx, `SELECT user_id FROM qq_activity_bindings WHERE qq=$1 AND status='active'`, inviter).Scan(&inviterUser)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	self := inviter == qq || (inviterUser.Valid && inviterUser.Int64 == userID)
	var newcomerStatus sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT status FROM qq_activity_rewards WHERE kind='newcomer' AND subject_qq=$1 AND subject_user_id=$2`, qq, userID).Scan(&newcomerStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	state, reason := "awaiting_evidence", "invitation_provenance_unknown"
	if evidence {
		state, reason = "awaiting_level", "invitee_level_unknown"
		if level != nil && *level < 16 {
			reason = "invitee_level_below_16"
		}
		if self {
			state, reason = "ineligible", "self_invitation"
		}
	}
	invitationQualifies := InvitationQualifies(evidence, inGroup, true, level, robot, self)
	if canQualify && newcomerStatus.Valid && invitationQualifies && WithinCampaign(c, inviteJoined) {
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_invitations SET qualified_at=COALESCE(qualified_at,NOW()) WHERE invitee_qq=$1`, qq); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_rewards(kind,recipient_qq,recipient_user_id,subject_qq,subject_user_id) VALUES('invitation',$1,$2,$3,$4) ON CONFLICT DO NOTHING`, inviter, inviterUser, qq, userID); err != nil {
			return err
		}
		state, reason = "pending", ""
		if !inviterUser.Valid {
			state = "awaiting_inviter"
		}
	} else if qualified.Valid {
		state, reason = "pending", ""
		if !inviterUser.Valid {
			state = "awaiting_inviter"
		}
	}
	if (invitationQualifies || qualified.Valid) && newcomerStatus.String != "paid" {
		state, reason = "awaiting_newcomer", "invitee_newcomer_unpaid"
	}
	_, err = tx.ExecContext(ctx, `UPDATE qq_activity_invitations SET invitee_user_id=$1,status=CASE WHEN status='paid' THEN status ELSE $2 END,reason=$3,updated_at=NOW() WHERE invitee_qq=$4`, userID, state, reason, qq)
	return err
}

// settle credits newcomers first, then invitations backed by the same QQ/user's
// paid newcomer reward. Both stages share one transaction, including notices.
// Paused campaigns retain rows; late inviter bindings may settle after the end.
func (s *Store) settle(ctx context.Context, tx *sql.Tx, c Config) ([]int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE qq_activity_rewards r SET recipient_user_id=b.user_id FROM qq_activity_bindings b WHERE r.recipient_qq=b.qq AND b.status='active' AND r.status='pending' AND r.recipient_user_id IS NULL`); err != nil {
		return nil, err
	}
	if c.Status != "active" && c.Status != "ended" {
		return nil, nil
	}
	var changed []int64
	for _, kind := range []string{"newcomer", "invitation"} {
		if kind == "invitation" {
			// Also bring historical pending invitations under the payment gate.
			if _, err := tx.ExecContext(ctx, `UPDATE qq_activity_invitations i SET status=CASE WHEN `+invitationNewcomerPaid+` THEN CASE WHEN r.recipient_user_id IS NULL THEN 'awaiting_inviter' ELSE 'pending' END ELSE 'awaiting_newcomer' END,reason=CASE WHEN `+invitationNewcomerPaid+` THEN '' ELSE 'invitee_newcomer_unpaid' END,updated_at=NOW() FROM qq_activity_rewards r WHERE r.kind='invitation' AND r.status='pending' AND i.invitee_qq=r.subject_qq AND i.invitee_user_id=r.subject_user_id AND i.status<>'paid'`); err != nil {
				return nil, err
			}
		}
		if (kind == "newcomer" && !c.NewcomerEnabled) || (kind == "invitation" && !c.InvitationEnabled) {
			continue
		}
		ids, err := s.settleKind(ctx, tx, kind)
		if err != nil {
			return nil, err
		}
		changed = append(changed, ids...)
	}
	return changed, nil
}

func (s *Store) settleKind(ctx context.Context, tx *sql.Tx, kind string) ([]int64, error) {
	gate := ""
	if kind == "invitation" {
		gate = " AND " + invitationNewcomerPaid
	}
	// Separate batches prevent blocked invitations (or disabled newcomers)
	// from occupying the limit and starving payable rewards.
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.kind,r.recipient_qq,b.user_id,r.subject_qq,r.subject_user_id FROM qq_activity_rewards r JOIN qq_activity_bindings b ON b.qq=r.recipient_qq AND b.status='active' AND b.user_id=r.recipient_user_id JOIN users u ON u.id=b.user_id AND u.deleted_at IS NULL AND u.status='active' WHERE r.status='pending' AND r.kind=$1`+gate+` ORDER BY b.user_id,r.id LIMIT 100 FOR UPDATE OF r`, kind)
	if err != nil {
		return nil, err
	}
	type payment struct {
		id, userID, subjectUser int64
		kind, qq, subjectQQ     string
	}
	var payments []payment
	for rows.Next() {
		var p payment
		if err = rows.Scan(&p.id, &p.kind, &p.qq, &p.userID, &p.subjectQQ, &p.subjectUser); err != nil {
			rows.Close()
			return nil, err
		}
		payments = append(payments, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	changed := []int64{}
	for _, p := range payments {
		var before, after float64
		if p.kind == "newcomer" {
			if err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance+2,updated_at=NOW() WHERE id=$1 AND status='active' AND deleted_at IS NULL RETURNING (balance-2)::double precision,balance::double precision`, p.userID).Scan(&before, &after); err != nil {
				return nil, err
			}
			changed = append(changed, p.userID)
		} else {
			code, err := randomToken()
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO user_affiliates(user_id,aff_code) VALUES($1,$2) ON CONFLICT(user_id) DO NOTHING`, p.userID, "QQ"+strings.ToUpper(code[:12])); err != nil {
				return nil, err
			}
			if err = tx.QueryRowContext(ctx, `UPDATE user_affiliates SET aff_quota=aff_quota+2,aff_history_quota=aff_history_quota+2,updated_at=NOW() WHERE user_id=$1 RETURNING (aff_quota-2)::double precision,aff_quota::double precision`, p.userID).Scan(&before, &after); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO user_affiliate_ledger(user_id,action,amount,source_user_id) VALUES($1,'qq_activity',2,$2)`, p.userID, p.subjectUser); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_invitations SET status='paid',reason='',updated_at=NOW() WHERE invitee_user_id=$1 AND invitee_qq=$2`, p.subjectUser, p.subjectQQ); err != nil {
				return nil, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_rewards SET status='paid',recipient_user_id=$1,paid_at=NOW(),balance_before=$2,balance_after=$3 WHERE id=$4`, p.userID, before, after, p.id); err != nil {
			return nil, err
		}
		// Payment notices belong to this newly paid reward, never to a binding
		// summary. Repeated commands cannot replay a historical reward notice.
		{
			var group string
			e := tx.QueryRowContext(ctx, `SELECT group_id FROM qq_activity_members WHERE qq=$1 AND in_group AND NOT is_robot ORDER BY last_seen_at DESC LIMIT 1`, p.qq).Scan(&group)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			if e == nil {
				text := "$2 体验额度已到账，可在官网余额中查看。"
				if p.kind == "invitation" {
					text = "一笔符合条件的邀请已完成！$2 邀请余额已到账，可在官网「邀请奖励」查看。"
				}
				if err = enqueue(ctx, tx, fmt.Sprintf("reward:%d", p.id), group, p.qq, text); err != nil {
					return nil, err
				}
			}
		}
	}
	return changed, nil
}

func (s *Store) invalidateUsers(ids []int64) {
	if s.invalidate == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seen := map[int64]bool{}
	for _, id := range ids {
		if !seen[id] {
			s.invalidate(ctx, id)
			seen[id] = true
		}
	}
}
