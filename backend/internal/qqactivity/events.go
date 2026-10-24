package qqactivity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) Process(ctx context.Context, e Event) error {
	if !ValidQQ(e.GroupID) || !ValidQQ(e.QQ) || len(e.ID) < 1 || len(e.ID) > 160 || e.Time <= 0 || e.Time > time.Now().Add(5*time.Minute).Unix() {
		return errors.New("无效的活动事件。")
	}
	switch e.Kind {
	case "join", "leave", "command", "level":
	default:
		return errors.New("不支持的活动事件。")
	}
	if e.Level != nil && (*e.Level <= 0 || *e.Level > 1000) {
		e.Level = nil
	}
	var changed []int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		if c.InitializedAt == nil {
			return ErrUnavailable
		}
		var bot string
		if err = tx.QueryRowContext(ctx, `SELECT bot_qq FROM qq_activity_groups WHERE group_id=$1`, e.GroupID).Scan(&bot); err != nil {
			return errors.New("未配置的活动群。")
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO qq_activity_events(event_id,group_id,qq,kind,occurred_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_id) DO NOTHING`, e.ID, e.GroupID, e.QQ, e.Kind, time.Unix(e.Time, 0))
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if e.Kind == "leave" {
			_, err = tx.ExecContext(ctx, `UPDATE qq_activity_members SET in_group=FALSE,last_seen_at=NOW() WHERE group_id=$1 AND qq=$2`, e.GroupID, e.QQ)
			return err
		}
		if !e.MemberConfirmed {
			return nil
		}
		// Membership history is permanent across leaves and activity groups.
		// Only a first observed join can establish an invitation opportunity.
		var knownMember bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_members WHERE qq=$1)`, e.QQ).Scan(&knownMember); err != nil {
			return err
		}
		joined := time.Unix(e.Time, 0)
		legacy := c.StartedAt == nil || !joined.After(*c.StartedAt)
		// A message is not a join event. Unknown members discovered by polling or
		// chat cannot prove they first joined after T0 and are conservatively marked.
		if e.Kind != "join" {
			legacy = true
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_members(group_id,qq,is_robot,legacy,first_joined_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(group_id,qq) DO UPDATE SET in_group=TRUE,is_robot=qq_activity_members.is_robot OR EXCLUDED.is_robot,last_seen_at=NOW()`, e.GroupID, e.QQ, e.Robot || e.QQ == bot, legacy, joined); err != nil {
			return err
		}
		if e.Robot || e.QQ == bot {
			return nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_members SET qq_level=$1,level_checked_at=NOW(),level_attempts=CASE WHEN $1::integer IS NULL THEN level_attempts+1 ELSE 0 END,next_level_retry_at=CASE WHEN $1::integer IS NOT NULL THEN NULL WHEN level_attempts=0 THEN NOW()+INTERVAL '1 minute' WHEN level_attempts=1 THEN NOW()+INTERVAL '5 minutes' WHEN level_attempts=2 THEN NOW()+INTERVAL '15 minutes' ELSE NULL END WHERE group_id=$2 AND qq=$3`, e.Level, e.GroupID, e.QQ); err != nil {
			return err
		}
		if e.Kind == "join" && !knownMember && WithinCampaign(c, joined) {
			trusted := e.EvidenceType == "direct_invite_pinned" && ValidQQ(e.InviterQQ) && e.InviterQQ != e.QQ && e.EvidenceKey != ""
			inviter, source, key := "", "", ""
			if trusted {
				inviter, source, key = e.InviterQQ, e.EvidenceType, e.EvidenceKey
			}
			// Later joins cannot replace even an unqualified first attribution.
			_, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_invitations(invitee_qq,inviter_qq,group_id,evidence_type,evidence_key,joined_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(invitee_qq) DO NOTHING`, e.QQ, inviter, e.GroupID, source, key, joined)
			if err != nil {
				return err
			}
		}
		var beforeBound bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_bindings WHERE qq=$1)`, e.QQ).Scan(&beforeBound); err != nil {
			return err
		}
		reply := ""
		if (e.Kind == "join" && c.AutoProcessJoin) || (e.Kind == "command" && e.Command == "绑定") {
			if reply == "" && !beforeBound {
				var nowBound bool
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_activity_bindings WHERE qq=$1)`, e.QQ).Scan(&nowBound); err != nil {
					return err
				}
				if !nowBound {
					candidate, state, err := findCandidate(ctx, tx, e.QQ)
					if err != nil {
						return err
					}
					if err = saveCandidate(ctx, tx, e.GroupID, e.QQ, candidate, state); err != nil {
						return err
					}
					switch state {
					case "matched":
						if err = bind(ctx, tx, e.QQ, candidate.ID, "matched_"+candidate.Source); err != nil {
							return err
						}
					case "unmatched":
						reply = Prompt("M01", map[string]string{"qq_email_mask": MaskEmail(e.QQ + "@qq.com"), "site_url": SiteURL})
					case "ambiguous":
						reply = Prompt("M04", map[string]string{"case_no": Hash(e.GroupID + ":" + e.QQ)[:12]})
					case "conflict":
						reply = Prompt("M06", map[string]string{"case_no": Hash(e.GroupID + ":" + e.QQ)[:12]})
					default:
						reply = Prompt("M07", map[string]string{"case_no": Hash(e.GroupID + ":" + e.QQ)[:12]})
					}
				}
			}
		}
		if err = s.reconcile(ctx, tx, e.QQ, c); err != nil {
			return err
		}
		quiet := e.QQ
		if e.Kind == "level" {
			quiet = ""
		}
		changed, err = s.settle(ctx, tx, c, quiet)
		if err != nil {
			return err
		}
		summary, err := summaryForQQ(ctx, tx, e.QQ, c)
		if err != nil {
			return err
		}
		if e.Kind == "command" {
			switch e.Command {
			case "活动帮助":
				reply = Prompt("G02", map[string]string{"qq_email_mask": MaskEmail(e.QQ + "@qq.com")})
			case "绑定状态":
				reply = renderSummary(summary)
			case "邀请奖励":
				reply = fmt.Sprintf("邀请奖励已到账：$%.2f\n已确认待发放：$%.2f\n待绑定领取：$%.2f\n详情：%s/qq-activity\n邀请余额转入使用余额，请按官网「邀请奖励」页面提示操作。", summary.InvitationPaid, summary.InvitationPending, summary.PendingInviterBinding, SiteURL)
				if !summary.Bound {
					reply += "\n发送「绑定」后可领取已确认的邀请奖励，邀请人本人不限等级。"
				}
			case "解绑", "更换绑定":
				reply = "如需解绑或更换关联账号，请联系管理员。已领取记录和资格记录会保留。"
			case "绑定":
				if reply == "" {
					reply = renderBinding(summary, beforeBound)
				}
			default:
				reply = Prompt("G03", nil)
			}
		} else if e.Kind == "join" && c.AutoProcessJoin && reply == "" {
			reply = renderBinding(summary, beforeBound)
		}
		if e.Kind != "level" && reply != "" {
			return enqueue(ctx, tx, "event:"+e.ID, e.GroupID, e.QQ, reply)
		}
		return nil
	})
	if err == nil {
		s.invalidateUsers(changed)
	}
	return err
}

func renderBinding(s Summary, already bool) string {
	prefix := "绑定成功！已关联官网账号："
	if already {
		prefix = "你已经绑定官网账号："
	}
	if !s.Bound {
		return "尚未绑定。请在活动群发送「绑定」开始办理。"
	}
	return prefix + s.Account + "。" + s.WelcomeText
}
func renderSummary(s Summary) string {
	status := "尚未绑定"
	if s.Bound {
		status = "已绑定"
	}
	text := fmt.Sprintf("绑定状态：%s\n官网账号：%s\n体验额度：%s\n邀请奖励已到账：$%.2f\n邀请奖励待发放：$%.2f\n待绑定领取：$%.2f\n详情：%s/qq-activity", status, s.Account, s.WelcomeText, s.InvitationPaid, s.InvitationPending, s.PendingInviterBinding, SiteURL)
	if !s.Bound {
		text += "\n发送「绑定」开始办理。"
	}
	return text
}

func (s *Store) Tick(ctx context.Context) error {
	var changed []int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		if err = s.autoBindMembers(ctx, tx, c); err != nil {
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

func (s *Store) LevelChecks(ctx context.Context) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON(m.qq) m.group_id,m.qq FROM qq_activity_members m JOIN qq_activity_bindings b ON b.qq=m.qq AND b.status='active' WHERE m.in_group AND NOT m.is_robot AND m.qq_level IS NULL AND m.level_attempts<4 AND m.next_level_retry_at<=NOW() AND (NOT m.legacy OR EXISTS(SELECT 1 FROM qq_activity_invitations i WHERE i.invitee_qq=m.qq AND i.qualified_at IS NULL AND i.inviter_qq<>'')) ORDER BY m.qq,m.last_seen_at DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		e.Kind = "level"
		if err = rows.Scan(&e.GroupID, &e.QQ); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) Outbox(ctx context.Context) ([]Outbox, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,group_id,qq,text FROM qq_activity_outbox WHERE delivered_at IS NULL AND attempts<8 AND next_attempt_at<=NOW() ORDER BY id LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Outbox{}
	for rows.Next() {
		var m Outbox
		if err = rows.Scan(&m.ID, &m.GroupID, &m.QQ, &m.Text); err != nil {
			return nil, err
		}
		m.Text = MaskText(m.Text)
		result = append(result, m)
	}
	return result, rows.Err()
}
func (s *Store) Acknowledge(ctx context.Context, id int64, delivered bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE qq_activity_outbox SET delivered_at=CASE WHEN $2 THEN NOW() ELSE delivered_at END,attempts=attempts+1,next_attempt_at=NOW()+INTERVAL '5 minutes' WHERE id=$1 AND delivered_at IS NULL`, id, delivered)
	return err
}

func RecognizeCommand(text string) (command, code string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "绑定":
		command = "绑定"
		if len(fields) > 1 {
			code = fields[1]
		}
		if len(fields) > 2 {
			code = "invalid"
		}
		return command, code, true
	case "绑定状态", "邀请奖励", "活动帮助", "解绑", "更换绑定":
		if len(fields) == 1 {
			return fields[0], "", true
		}
	}
	return
}
