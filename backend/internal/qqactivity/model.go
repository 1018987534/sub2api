// Package qqactivity implements the QQ activity independently of the existing referral hierarchy.
package qqactivity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
)

const SiteURL = "https://xiaohondou.com"
const NotEligible = "当前暂不符合体验额度领取条件。"
const InvitationNotEligible = "当前暂不符合邀请奖励领取条件。"
const advisoryLock int64 = 0x51514143544956

var ErrUnavailable = errors.New("活动暂时不可用，请稍后重试。")
var ErrClaim = errors.New("确认信息无效或已过期，请回到活动群重新发送「绑定」。")
var qqPattern = regexp.MustCompile(`^[1-9][0-9]{4,14}$`)
var codePattern = regexp.MustCompile(`^[0-9]{6}$`)
var emailPattern = regexp.MustCompile(`[A-Za-z0-9.!#$%&'*+/=?^_\x60{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

type MemberSnapshot struct {
	QQ       string `json:"qq"`
	Robot    bool   `json:"robot"`
	JoinedAt int64  `json:"joined_at"`
}
type GroupSnapshot struct {
	GroupID       string           `json:"group_id"`
	BotQQ         string           `json:"bot_qq"`
	ExpectedCount int              `json:"expected_count"`
	Members       []MemberSnapshot `json:"members"`
}
type Event struct {
	ID              string `json:"id"`
	GroupID         string `json:"group_id"`
	QQ              string `json:"qq"`
	Kind            string `json:"kind"` // join, leave, command, level
	Time            int64  `json:"time"`
	Command         string `json:"command,omitempty"`
	Code            string `json:"code,omitempty"`
	Level           *int   `json:"level,omitempty"` // only get_stranger_info.qqLevel; never the member-list level
	MemberConfirmed bool   `json:"member_confirmed"`
	Robot           bool   `json:"robot,omitempty"`
	InviterQQ       string `json:"inviter_qq,omitempty"`
	EvidenceType    string `json:"evidence_type,omitempty"`
	EvidenceKey     string `json:"evidence_key,omitempty"`
}
type Config struct {
	Status               string     `json:"status"`
	InitializedAt        *time.Time `json:"initialized_at"`
	StartedAt            *time.Time `json:"started_at"`
	EndedAt              *time.Time `json:"ended_at"`
	NewcomerEnabled      bool       `json:"newcomer_enabled"`
	InvitationEnabled    bool       `json:"invitation_enabled"`
	AutoProcessJoin      bool       `json:"auto_process_join"`
	GroupMessagesEnabled bool       `json:"group_messages_enabled"`
}
type Summary struct {
	Config                Config   `json:"config"`
	Bound                 bool     `json:"bound"`
	QQ                    string   `json:"qq,omitempty"`
	Account               string   `json:"account"`
	WelcomeStatus         string   `json:"welcome_status"`
	WelcomeText           string   `json:"welcome_text"`
	InvitationPaid        float64  `json:"invitation_paid"`
	InvitationPending     float64  `json:"invitation_pending"`
	PendingInviterBinding float64  `json:"pending_inviter_binding"`
	Rewards               []Reward `json:"rewards"`
}
type Reward struct {
	ID     int64      `json:"id"`
	Kind   string     `json:"kind"`
	Amount float64    `json:"amount"`
	Status string     `json:"status"`
	PaidAt *time.Time `json:"paid_at,omitempty"`
}
type Outbox struct {
	ID      int64  `json:"id"`
	GroupID string `json:"group_id"`
	QQ      string `json:"qq"`
	Text    string `json:"text"`
}
type Candidate struct {
	ID     int64
	Email  string
	Status string
	Source string
}

func ValidQQ(value string) bool { return qqPattern.MatchString(value) }
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func MaskEmail(email string) string {
	email = strings.TrimSpace(email)
	i := strings.LastIndexByte(email, '@')
	if i < 1 || i == len(email)-1 {
		return "***"
	}
	local := []rune(email[:i])
	domain := strings.ToLower(email[i+1:])
	if len(local) >= 5 {
		return string(local[:2]) + "****" + string(local[len(local)-2:]) + "@" + domain
	}
	return string(local[:1]) + "***@" + domain
}
func MaskText(text string) string { return emailPattern.ReplaceAllStringFunc(text, MaskEmail) }

// Inviter level is deliberately absent: only the actual invitee's level matters.
func InvitationQualifies(evidence bool, joined bool, bound bool, level *int, robot bool, self bool) bool {
	return evidence && joined && bound && level != nil && *level >= 16 && !robot && !self
}
func NewcomerQualifies(legacy bool, joinedAfterStart bool, accountAfterStart bool, level *int, robot bool) bool {
	return !legacy && joinedAfterStart && accountAfterStart && level != nil && *level >= 16 && !robot
}

func WithinCampaign(c Config, at time.Time) bool {
	return c.StartedAt != nil && !at.Before(*c.StartedAt) && (c.EndedAt == nil || !at.After(*c.EndedAt)) && c.Status != "preparing"
}
