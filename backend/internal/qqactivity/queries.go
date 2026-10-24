package qqactivity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func summaryForQQ(ctx context.Context, q queryer, qq string, c Config) (s Summary, err error) {
	s = Summary{Config: c, Account: "—", QQ: qq, WelcomeStatus: "not_bound", WelcomeText: "尚未绑定。", Rewards: []Reward{}}
	var userID int64
	var email, status string
	var registered time.Time
	var baseline bool
	err = q.QueryRowContext(ctx, `SELECT b.user_id,u.email,u.status,u.created_at,EXISTS(SELECT 1 FROM qq_activity_baseline_users x WHERE x.user_id=u.id OR lower(btrim(x.email))=lower(btrim(u.email))) FROM qq_activity_bindings b JOIN users u ON u.id=b.user_id AND u.deleted_at IS NULL WHERE b.qq=$1`, qq).Scan(&userID, &email, &status, &registered, &baseline)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	if err == nil {
		s.Bound = true
		s.Account = MaskEmail(email)
		s.WelcomeStatus = "not_eligible"
		s.WelcomeText = NotEligible
		level, legacy, _, _, _, e := memberState(ctx, q, qq)
		if e != nil {
			return s, e
		}
		if status == "active" && !baseline && !legacy && c.StartedAt != nil && registered.After(*c.StartedAt) && level == nil {
			s.WelcomeStatus = "verifying"
			s.WelcomeText = "本次体验额度领取资格正在核验，暂未发放。系统会继续处理，你也可以稍后发送「绑定状态」查询。"
		}
		if c.Status == "preparing" {
			s.WelcomeStatus = "preparing"
			s.WelcomeText = "活动正在准备，奖励暂未开放。"
		}
		if c.Status == "ended" {
			s.WelcomeText = NotEligible
		}
		rows, e := q.QueryContext(ctx, `SELECT id,kind,amount::double precision,status,paid_at FROM qq_activity_rewards WHERE recipient_qq=$1 ORDER BY id DESC LIMIT 100`, qq)
		if e != nil {
			return s, e
		}
		for rows.Next() {
			var r Reward
			if e = rows.Scan(&r.ID, &r.Kind, &r.Amount, &r.Status, &r.PaidAt); e != nil {
				rows.Close()
				return s, e
			}
			s.Rewards = append(s.Rewards, r)
			if r.Kind == "newcomer" {
				s.WelcomeStatus = r.Status
				if r.Status == "paid" {
					s.WelcomeText = "$2 体验额度已到账，本活动不会重复发放。"
				} else {
					s.WelcomeText = "$2 体验额度已登记为待发放，暂未到账。系统会继续处理，无需重复绑定。"
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return s, e
		}
	}
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount) FILTER(WHERE status='paid'),0)::double precision,COALESCE(SUM(amount) FILTER(WHERE status='pending' AND recipient_user_id IS NOT NULL),0)::double precision,COALESCE(SUM(amount) FILTER(WHERE status='pending' AND recipient_user_id IS NULL),0)::double precision FROM qq_activity_rewards WHERE kind='invitation' AND recipient_qq=$1`, qq).Scan(&s.InvitationPaid, &s.InvitationPending, &s.PendingInviterBinding)
	return
}
func (s *Store) UserSummary(ctx context.Context, userID int64) (Summary, error) {
	c, err := readConfig(ctx, s.db)
	if err != nil {
		return Summary{}, err
	}
	var qq string
	err = s.db.QueryRowContext(ctx, `SELECT qq FROM qq_activity_bindings WHERE user_id=$1`, userID).Scan(&qq)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{Config: c, Account: "—", WelcomeStatus: "not_bound", WelcomeText: "系统会自动匹配活动群成员；也可在群内发送「绑定」立即重试。", Rewards: []Reward{}}, nil
	}
	if err != nil {
		return Summary{}, err
	}
	return summaryForQQ(ctx, s.db, qq, c)
}

type AdminMember struct {
	GroupID    string `json:"group_id"`
	QQ         string `json:"qq"`
	InGroup    bool   `json:"in_group"`
	Robot      bool   `json:"robot"`
	Legacy     bool   `json:"legacy"` // restricted audit field; never a user-facing explanation
	MatchState string `json:"match_state"`
	Account    string `json:"account"`
	Level      *int   `json:"level"`
	Bound      bool   `json:"bound"`
}
type AdminInvitation struct {
	Invitee  string `json:"invitee"`
	Inviter  string `json:"inviter"`
	GroupID  string `json:"group_id"`
	Evidence string `json:"evidence"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
}
type AdminReport struct {
	Config         Config            `json:"config"`
	BaselineUsers  int64             `json:"baseline_users"`
	MembersCount   int64             `json:"members_count"`
	BindingsCount  int64             `json:"bindings_count"`
	NewcomerPaid   float64           `json:"newcomer_paid"`
	InvitationPaid float64           `json:"invitation_paid"`
	PendingAmount  float64           `json:"pending_amount"`
	UnsentMessages int64             `json:"unsent_messages"`
	Members        []AdminMember     `json:"members"`
	Invitations    []AdminInvitation `json:"invitations"`
}

func (s *Store) AdminReport(ctx context.Context) (r AdminReport, err error) {
	r.Config, err = readConfig(ctx, s.db)
	if err != nil {
		return
	}
	r.Members = []AdminMember{}
	r.Invitations = []AdminInvitation{}
	err = s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM qq_activity_baseline_users),(SELECT COUNT(*) FROM qq_activity_members),(SELECT COUNT(*) FROM qq_activity_bindings),COALESCE(SUM(amount) FILTER(WHERE kind='newcomer' AND status='paid'),0)::double precision,COALESCE(SUM(amount) FILTER(WHERE kind='invitation' AND status='paid'),0)::double precision,COALESCE(SUM(amount) FILTER(WHERE status='pending'),0)::double precision,(SELECT COUNT(*) FROM qq_activity_outbox WHERE delivered_at IS NULL) FROM qq_activity_rewards`).Scan(&r.BaselineUsers, &r.MembersCount, &r.BindingsCount, &r.NewcomerPaid, &r.InvitationPaid, &r.PendingAmount, &r.UnsentMessages)
	if err != nil {
		return
	}
	rows, e := s.db.QueryContext(ctx, `SELECT m.group_id,m.qq,m.in_group,m.is_robot,m.legacy,m.match_state,COALESCE(u.email,''),m.qq_level,b.qq IS NOT NULL FROM qq_activity_members m LEFT JOIN qq_activity_bindings b ON b.qq=m.qq LEFT JOIN users u ON u.id=COALESCE(b.user_id,m.candidate_user_id) ORDER BY m.group_id,m.qq LIMIT 5000`)
	if e != nil {
		err = e
		return
	}
	for rows.Next() {
		var m AdminMember
		var email string
		if e = rows.Scan(&m.GroupID, &m.QQ, &m.InGroup, &m.Robot, &m.Legacy, &m.MatchState, &email, &m.Level, &m.Bound); e != nil {
			rows.Close()
			err = e
			return
		}
		m.Account = MaskEmail(email)
		r.Members = append(r.Members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	rows, err = s.db.QueryContext(ctx, `SELECT invitee_qq,inviter_qq,group_id,evidence_type,status,reason FROM qq_activity_invitations ORDER BY updated_at DESC LIMIT 500`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var i AdminInvitation
		if err = rows.Scan(&i.Invitee, &i.Inviter, &i.GroupID, &i.Evidence, &i.Status, &i.Reason); err != nil {
			return
		}
		r.Invitations = append(r.Invitations, i)
	}
	err = rows.Err()
	return
}

func (s *Store) Control(ctx context.Context, actor, status string) error {
	var changed []int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		if c.StartedAt == nil {
			return errors.New("请先通过完整群名单初始化并开启活动。")
		}
		if status != "active" && status != "paused" && status != "ended" {
			return errors.New("无效的活动状态。")
		}
		if c.Status == "ended" && status != "ended" {
			return errors.New("已结束的活动不能重新开启。")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_config SET status=$1,ended_at=CASE WHEN $1='ended' THEN COALESCE(ended_at,NOW()) ELSE ended_at END,updated_at=NOW() WHERE id=1`, status); err != nil {
			return err
		}
		if err = audit(ctx, tx, actor, "status", status, map[string]any{"before": c.Status}); err != nil {
			return err
		}
		c, err = readConfig(ctx, tx)
		if err != nil {
			return err
		}
		changed, err = s.settle(ctx, tx, c, "")
		return err
	})
	if err == nil {
		s.invalidateUsers(changed)
	}
	return err
}

type SettingsUpdate struct {
	NewcomerEnabled      *bool `json:"newcomer_enabled"`
	InvitationEnabled    *bool `json:"invitation_enabled"`
	AutoProcessJoin      *bool `json:"auto_process_join"`
	GroupMessagesEnabled *bool `json:"group_messages_enabled"`
}

func (s *Store) UpdateSettings(ctx context.Context, actor string, update SettingsUpdate) error {
	var changed []int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE qq_activity_config SET newcomer_enabled=COALESCE($1,newcomer_enabled),invitation_enabled=COALESCE($2,invitation_enabled),auto_process_join=COALESCE($3,auto_process_join),group_messages_enabled=COALESCE($4,group_messages_enabled),updated_at=NOW() WHERE id=1`, update.NewcomerEnabled, update.InvitationEnabled, update.AutoProcessJoin, update.GroupMessagesEnabled)
		if err != nil {
			return err
		}
		if update.GroupMessagesEnabled != nil && !*update.GroupMessagesEnabled {
			if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_outbox SET delivered_at=NOW() WHERE delivered_at IS NULL`); err != nil {
				return err
			}
		}
		if err = audit(ctx, tx, actor, "settings", "", update); err != nil {
			return err
		}
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		changed, err = s.settle(ctx, tx, c, "")
		return err
	})
	if err == nil {
		s.invalidateUsers(changed)
	}
	return err
}

func (s *Store) VerifyInvitation(ctx context.Context, actor, invitee, inviter, group, evidence string) error {
	if !ValidQQ(invitee) || !ValidQQ(inviter) || !ValidQQ(group) || invitee == inviter || len(evidence) < 8 || len(evidence) > 2000 {
		return errors.New("请提供完整的邀请双方、群号及可核验的邀请证据。")
	}
	var changed []int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		// Manual review may establish missing evidence for the first join only.
		// It cannot turn a historical member or a rejoin into a new opportunity.
		var joined time.Time
		var inGroup bool
		if err = tx.QueryRowContext(ctx, `SELECT first_joined_at,in_group FROM qq_activity_members WHERE group_id=$1 AND qq=$2`, group, invitee).Scan(&joined, &inGroup); err != nil {
			return errors.New("未找到实际入群记录。")
		}
		if !inGroup {
			return errors.New("请先核对受邀成员的当前入群状态。")
		}
		_, legacy, robot, _, firstJoined, err := memberState(ctx, tx, invitee)
		if err != nil {
			return err
		}
		if legacy || robot || c.StartedAt == nil || !firstJoined.After(*c.StartedAt) {
			return errors.New("该成员当前不符合邀请奖励核验条件。")
		}
		var firstGroup, firstInviter string
		if err = tx.QueryRowContext(ctx, `SELECT group_id,inviter_qq FROM qq_activity_invitations WHERE invitee_qq=$1`, invitee).Scan(&firstGroup, &firstInviter); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("未找到首次入群邀请记录，不能从重复入群创建邀请奖励。")
			}
			return err
		}
		if firstGroup != group || (firstInviter != "" && firstInviter != inviter) {
			return errors.New("该成员已有首次入群记录，不能更换邀请归属。")
		}
		var paid bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_rewards WHERE kind='invitation' AND subject_qq=$1)`, invitee).Scan(&paid); err != nil {
			return err
		}
		if paid {
			return errors.New("该成员的邀请奖励已经确认，不能更换邀请归属。")
		}
		if !WithinCampaign(c, time.Now()) {
			return errors.New("当前不能新增本期邀请奖励资格。")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_invitations SET inviter_qq=$1,evidence_type='admin_verified',evidence_key=$2,updated_at=NOW() WHERE invitee_qq=$3`, inviter, Hash(evidence), invitee); err != nil {
			return err
		}
		if err = audit(ctx, tx, actor, "verify_invitation", invitee, map[string]any{"inviter": inviter, "evidence": MaskText(evidence)}); err != nil {
			return err
		}
		if err = s.reconcile(ctx, tx, invitee, c); err != nil {
			return err
		}
		changed, err = s.settle(ctx, tx, c, "")
		return err
	})
	if err == nil {
		s.invalidateUsers(changed)
	}
	return err
}

func (s *Store) BridgeStatus(ctx context.Context) (map[string]any, error) {
	r, err := s.AdminReport(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"config": r.Config, "baseline_users": r.BaselineUsers, "members": r.MembersCount, "bindings": r.BindingsCount, "newcomer_paid": r.NewcomerPaid, "invitation_paid": r.InvitationPaid, "pending_amount": r.PendingAmount, "unsent_messages": r.UnsentMessages}, nil
}

func (s *Store) LockBinding(ctx context.Context, actor, qq, reason string) error {
	if !ValidQQ(qq) || len(reason) < 4 || len(reason) > 1000 {
		return fmt.Errorf("请填写 QQ 与处理原因。")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE qq_activity_bindings SET status='locked' WHERE qq=$1`, qq)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return errors.New("未找到绑定记录。")
		}
		return audit(ctx, tx, actor, "lock_binding", qq, map[string]string{"reason": MaskText(reason)})
	})
}
