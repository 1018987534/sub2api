package qqactivity

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

//go:embed prompts.json
var promptFiles embed.FS
var prompts = func() map[string]string {
	data, _ := promptFiles.ReadFile("prompts.json")
	var catalogue struct {
		Prompts []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(data, &catalogue); err != nil {
		panic(err)
	}
	result := make(map[string]string)
	for _, p := range catalogue.Prompts {
		result[p.ID] = p.Text
	}
	return result
}()

func Prompt(id string, vars map[string]string) string {
	text := prompts[id]
	for key, value := range vars {
		text = strings.ReplaceAll(text, "{"+key+"}", value)
	}
	return MaskText(text)
}

type Store struct {
	db         *sql.DB
	invalidate func(context.Context, int64)
}

func NewStore(db *sql.DB, invalidate func(context.Context, int64)) *Store {
	return &Store{db: db, invalidate: invalidate}
}

func (s *Store) transaction(ctx context.Context, action func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLock); err != nil {
		return err
	}
	if err = action(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readConfig(ctx context.Context, q queryer) (c Config, err error) {
	err = q.QueryRowContext(ctx, `SELECT status,initialized_at,started_at,ended_at,newcomer_enabled,invitation_enabled,auto_process_join,group_messages_enabled FROM qq_activity_config WHERE id=1`).Scan(&c.Status, &c.InitializedAt, &c.StartedAt, &c.EndedAt, &c.NewcomerEnabled, &c.InvitationEnabled, &c.AutoProcessJoin, &c.GroupMessagesEnabled)
	return
}
func (s *Store) Config(ctx context.Context) (Config, error) { return readConfig(ctx, s.db) }

func validateSnapshots(groups []GroupSnapshot) error {
	if len(groups) == 0 || len(groups) > 10 {
		return errors.New("需要完整活动群名单。")
	}
	seenGroups := map[string]bool{}
	for _, g := range groups {
		if !ValidQQ(g.GroupID) || !ValidQQ(g.BotQQ) || seenGroups[g.GroupID] || len(g.Members) == 0 || len(g.Members) != g.ExpectedCount {
			return errors.New("群名单数量不一致，暂不开启活动。")
		}
		seenGroups[g.GroupID] = true
		seen := map[string]bool{}
		for _, m := range g.Members {
			if !ValidQQ(m.QQ) || seen[m.QQ] {
				return errors.New("群名单存在无效或重复成员。")
			}
			seen[m.QQ] = true
		}
		if !seen[g.BotQQ] {
			return errors.New("活动机器人不在完整群名单中。")
		}
	}
	return nil
}

// Initialize is silent, append-only, and never issues rewards. Activation takes a
// fresh full snapshot in this same transaction; re-running cannot move T0.
func (s *Store) Initialize(ctx context.Context, groups []GroupSnapshot, activate bool) error {
	if err := validateSnapshots(groups); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := readConfig(ctx, tx)
		if err != nil {
			return err
		}
		if c.StartedAt != nil {
			return errors.New("活动已经开启，不能重新初始化或覆盖基线。")
		}
		var now time.Time
		if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		for _, g := range groups {
			if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_groups(group_id,bot_qq) VALUES($1,$2) ON CONFLICT(group_id) DO UPDATE SET bot_qq=EXCLUDED.bot_qq`, g.GroupID, g.BotQQ); err != nil {
				return err
			}
			for _, m := range g.Members {
				joined := time.Unix(m.JoinedAt, 0)
				if m.JoinedAt <= 0 || joined.After(now) {
					joined = now
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_members(group_id,qq,is_robot,legacy,first_joined_at) VALUES($1,$2,$3,TRUE,$4) ON CONFLICT(group_id,qq) DO UPDATE SET legacy=TRUE,in_group=TRUE,is_robot=qq_activity_members.is_robot OR EXCLUDED.is_robot,last_seen_at=NOW()`, g.GroupID, m.QQ, m.Robot || m.QQ == g.BotQQ, joined); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_baseline_users(user_id,registered_at,email,username) SELECT id,created_at,email,COALESCE(username,'') FROM users ON CONFLICT(user_id) DO NOTHING`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_members SET legacy=TRUE`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qq_activity_config SET initialized_at=COALESCE(initialized_at,$1),status=CASE WHEN $2 THEN 'active' ELSE 'preparing' END,started_at=CASE WHEN $2 THEN $1 ELSE NULL END,updated_at=NOW() WHERE id=1`, now, activate); err != nil {
			return err
		}
		// Match every recorded member; ambiguous or unverified candidates remain explicit.
		rows, err := tx.QueryContext(ctx, `SELECT group_id,qq,is_robot FROM qq_activity_members ORDER BY group_id,qq`)
		if err != nil {
			return err
		}
		type member struct {
			group, qq string
			robot     bool
		}
		var members []member
		for rows.Next() {
			var m member
			if err = rows.Scan(&m.group, &m.qq, &m.robot); err != nil {
				rows.Close()
				return err
			}
			members = append(members, m)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, m := range members {
			if m.robot {
				continue
			}
			candidate, state, err := findCandidate(ctx, tx, m.qq)
			if err != nil {
				return err
			}
			if err = saveCandidate(ctx, tx, m.group, m.qq, candidate, state); err != nil {
				return err
			}
			if candidate != nil && state == "verified" {
				if err = bind(ctx, tx, m.qq, candidate.ID, "verified_email"); err != nil {
					return err
				}
			}
		}
		return audit(ctx, tx, "bridge", "initialize", "", map[string]any{"groups": len(groups), "activate": activate, "members": len(members)})
	})
}

func findCandidate(ctx context.Context, q queryer, qq string) (*Candidate, string, error) {
	email := qq + "@qq.com"
	// A historical verified_at alone is NOT ownership proof: migration 109 backfilled it.
	query := `SELECT u.id,u.email,u.status,EXISTS(SELECT 1 FROM auth_identities a WHERE a.user_id=u.id AND a.provider_type='email' AND lower(btrim(a.provider_subject))=$1 AND a.verified_at IS NOT NULL AND a.metadata->>'email_ownership_verified'='true' AND NOT (a.metadata ? 'backfill_source')) FROM users u WHERE u.deleted_at IS NULL AND lower(btrim(u.email))=$1 ORDER BY u.id LIMIT 3`
	read := func(query, source string) ([]Candidate, error) {
		rows, err := q.QueryContext(ctx, query, email)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := []Candidate{}
		for rows.Next() {
			var c Candidate
			c.Source = source
			if err := rows.Scan(&c.ID, &c.Email, &c.Status, &c.Verified); err != nil {
				return nil, err
			}
			result = append(result, c)
		}
		return result, rows.Err()
	}
	candidates, err := read(query, "email")
	if err != nil {
		return nil, "", err
	}
	if len(candidates) == 0 {
		candidates, err = read(`SELECT id,email,status,FALSE FROM users WHERE deleted_at IS NULL AND lower(btrim(username))=$1 ORDER BY id LIMIT 3`, "username")
		if err != nil {
			return nil, "", err
		}
	}
	if len(candidates) == 0 {
		return nil, "unmatched", nil
	}
	if len(candidates) > 1 {
		return nil, "ambiguous", nil
	}
	c := candidates[0]
	if c.Status != "active" {
		return &c, "account_unavailable", nil
	}
	var other string
	err = q.QueryRowContext(ctx, `SELECT qq FROM qq_activity_bindings WHERE user_id=$1`, c.ID).Scan(&other)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	if err == nil && other != qq {
		return &c, "conflict", nil
	}
	if c.Source == "email" && c.Verified {
		return &c, "verified", nil
	}
	return &c, "confirmation_required", nil
}
func saveCandidate(ctx context.Context, tx *sql.Tx, group, qq string, c *Candidate, state string) error {
	var id any
	source := ""
	if c != nil {
		id = c.ID
		source = c.Source
	}
	_, err := tx.ExecContext(ctx, `UPDATE qq_activity_members SET candidate_user_id=$1,match_source=$2,match_state=$3 WHERE group_id=$4 AND qq=$5`, id, source, state, group, qq)
	return err
}
func bind(ctx context.Context, tx *sql.Tx, qq string, userID int64, source string) error {
	var id int64
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&id, &status)
	if err != nil {
		return err
	}
	if status != "active" {
		return errors.New("账号当前无法绑定，请联系管理员。")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_bindings(qq,user_id,source) VALUES($1,$2,$3) ON CONFLICT(qq) DO NOTHING`, qq, userID, source)
	if err != nil {
		return err
	}
	var boundID int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM qq_activity_bindings WHERE qq=$1`, qq).Scan(&boundID)
	if err != nil {
		return err
	}
	if boundID != userID {
		return errors.New("当前 QQ 已有关联账号，请联系管理员。")
	}
	_, err = tx.ExecContext(ctx, `UPDATE qq_activity_members SET candidate_user_id=$1,match_state='bound' WHERE qq=$2`, userID, qq)
	return err
}
func audit(ctx context.Context, tx *sql.Tx, actor, action, subject string, detail any) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_audit(actor,action,subject,detail) VALUES($1,$2,$3,$4)`, actor, action, subject, string(data))
	return err
}
func enqueue(ctx context.Context, tx *sql.Tx, key, group, qq, text string) error {
	if text == "" {
		return nil
	}
	if strings.Contains(text, "{") {
		return fmt.Errorf("unresolved message template")
	}
	c, err := readConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !c.GroupMessagesEnabled {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_activity_outbox(dedup_key,group_id,qq,text) VALUES($1,$2,$3,$4) ON CONFLICT(dedup_key) DO NOTHING`, key, group, qq, MaskText(text))
	return err
}
