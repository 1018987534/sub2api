package qqactivity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRewardRuleMatrix(t *testing.T) {
	for _, level := range []int{0, 7, 15, 16, 21, 109} {
		var l *int
		if level > 0 {
			v := level
			l = &v
		}
		for _, legacy := range []bool{false, true} {
			newcomer := NewcomerQualifies(legacy, true, true, l, false)
			if newcomer != (!legacy && level >= 16) {
				t.Fatalf("newcomer legacy=%v level=%d got=%v", legacy, level, newcomer)
			}
			// No inviter-level argument: old or low-level inviters are eligible.
			invitation := InvitationQualifies(true, true, true, l, false, false)
			if invitation != (level >= 16) {
				t.Fatalf("invitation legacy=%v invitee_level=%d", legacy, level)
			}
		}
	}
	level := 21
	if InvitationQualifies(false, true, true, &level, false, false) || InvitationQualifies(true, false, true, &level, false, false) || InvitationQualifies(true, true, false, &level, false, false) || InvitationQualifies(true, true, true, &level, true, false) || InvitationQualifies(true, true, true, &level, false, true) {
		t.Fatal("missing evidence, missing join/binding, robot or self invitation qualified")
	}
}
func TestMaskAllEmailShapes(t *testing.T) {
	for _, email := range []string{"12345678@qq.com", "member@example.com", "a@qq.com", "12345@QQ.COM", "email-shaped-username@example.org"} {
		masked := MaskEmail(email)
		if masked == email || !strings.Contains(masked, "*") {
			t.Fatalf("email not masked: %q", masked)
		}
		text := MaskText("账号：" + email + "；用户名：" + email)
		if strings.Contains(strings.ToLower(text), strings.ToLower(email)) {
			t.Fatal("unmasked email in text")
		}
		if MaskText(text) != text {
			t.Fatal("masking must be idempotent")
		}
	}
}
func TestPromptCataloguePrivacyAndCompleteness(t *testing.T) {
	if len(prompts) != 99 {
		t.Fatalf("approved prompt count=%d", len(prompts))
	}
	vars := map[string]string{"bind_url": "https://xiaohondou.com/qq-activity?claim=fixture", "qq_email_mask": MaskEmail("12345678@qq.com"), "case_no": "TESTCASE", "account_mask": MaskEmail("12345678@qq.com"), "site_url": SiteURL}
	for _, id := range []string{"M01", "M02", "M03", "M04", "M06", "M07", "M08", "M09", "M10", "M12", "G02", "G03"} {
		text := Prompt(id, vars)
		if text == "" || strings.Contains(text, "{") {
			t.Fatalf("unresolved prompt %s", id)
		}
		if strings.Contains(text, "12345678@qq.com") {
			t.Fatal("raw QQ email displayed")
		}
	}
	summary := Summary{Bound: true, Account: MaskEmail("12345678@qq.com"), WelcomeText: NotEligible}
	for _, text := range []string{renderBinding(summary, false), renderBinding(summary, true), renderSummary(summary)} {
		if strings.Contains(text, "已有用户") || strings.Contains(text, "活动前") || strings.Contains(text, "等级不足") {
			t.Fatal("individual ineligibility reason disclosed")
		}
	}
	data, _ := json.Marshal(summary)
	if strings.Contains(string(data), "12345678@qq.com") {
		t.Fatal("raw email in public JSON")
	}
}
func TestCampaignWindowAndSnapshotValidation(t *testing.T) {
	start := time.Now()
	end := start.Add(time.Hour)
	c := Config{Status: "active", StartedAt: &start, EndedAt: &end}
	if WithinCampaign(c, start.Add(-time.Second)) || !WithinCampaign(c, start) || WithinCampaign(c, end.Add(time.Second)) {
		t.Fatal("campaign boundary incorrect")
	}
	g := GroupSnapshot{GroupID: "12345678", BotQQ: "23456789", ExpectedCount: 1, Members: []MemberSnapshot{{QQ: "23456789", Robot: true}}}
	if validateSnapshots([]GroupSnapshot{g}) != nil {
		t.Fatal("valid snapshot rejected")
	}
	g.ExpectedCount = 2
	if validateSnapshots([]GroupSnapshot{g}) == nil {
		t.Fatal("partial member list accepted")
	}
}
func TestRecognizeCommands(t *testing.T) {
	for _, text := range []string{"绑定", " 绑定 123456 ", "绑定状态", "邀请奖励", "活动帮助"} {
		if _, _, ok := RecognizeCommand(text); !ok {
			t.Fatal("command rejected")
		}
	}
	for _, text := range []string{"帮我绑定一下", "普通聊天", "绑定状态 hi"} {
		if _, _, ok := RecognizeCommand(text); ok {
			t.Fatal("ordinary message interpreted as command")
		}
	}
}
