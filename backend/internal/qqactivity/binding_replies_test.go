package qqactivity

import (
	"strings"
	"testing"
)

func TestBindingRepliesOnlyConfirmAccount(t *testing.T) {
	for _, rewardState := range []string{"not_eligible", "paid", "pending", "verifying", "preparing", "ended"} {
		t.Run(rewardState, func(t *testing.T) {
			s := Summary{Bound: true, Account: "22****01@qq.com", WelcomeStatus: rewardState, WelcomeText: "$2 体验额度状态：" + rewardState, InvitationPaid: 2, InvitationPending: 2}
			if got := renderBinding(s, false); got != "绑定成功！已关联官网账号：22****01@qq.com。" {
				t.Fatalf("unexpected first binding reply: %q", got)
			}
			if got := renderBinding(s, true); got != "你已经绑定官网账号：22****01@qq.com。" {
				t.Fatalf("unexpected repeat binding reply: %q", got)
			}
			if got := renderSummary(s); strings.Contains(got, "$2") || strings.Contains(got, "资格") || strings.Contains(got, "体验额度") {
				t.Fatalf("binding status leaked reward information: %q", got)
			}
		})
	}
}

func TestOrdinaryPromptCopiesDoNotAnnounceRewards(t *testing.T) {
	for _, id := range []string{"G01", "G02", "B02", "B03", "B04", "B05", "B06", "B07", "B08", "B09", "B11", "B12", "B14", "B15", "B16", "B17", "I02", "I04", "I07", "I08", "I14", "I15", "I16", "I17", "I19", "E06", "E08"} {
		if strings.Contains(prompts[id], "$2") {
			t.Errorf("ordinary prompt %s still announces reward amount", id)
		}
	}
}
