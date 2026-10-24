package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectInvitationNeverUsesApprovalOperator(t *testing.T) {
	e := rawEvent{NoticeType: "group_increase", SubType: "approve", UserID: json.Number("12345678"), OperatorID: json.Number("23456789"), GroupID: json.Number("34567890"), Time: 123}
	if qq, _, _ := directInviter(e, "v4.18.41"); qq != "" {
		t.Fatal("approver misattributed as inviter")
	}
	e.SubType = "invite"
	if qq, source, key := directInviter(e, "v4.18.41"); qq != "23456789" || source == "" || key == "" {
		t.Fatal("pinned direct invitation not preserved")
	}
	if qq, _, _ := directInviter(e, "new-unverified-version"); qq != "" {
		t.Fatal("unverified source allowed auto payout")
	}
	e.OperatorID = e.UserID
	if qq, _, _ := directInviter(e, "v4.18.41"); qq != "" {
		t.Fatal("self invitation allowed")
	}
}

func TestDepartedMemberDoesNotBlockQueue(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/get_group_member_info" {
			_, _ = w.Write([]byte(`{"status":"failed","retcode":1200}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":[{"user_id":23456789}]}`))
	}))
	defer s.Close()
	b := &bot{cfg: configuration{HTTPURL: s.URL}, client: s.Client()}
	if found, err := b.membership(context.Background(), "34567890", "12345678"); err != nil || found {
		t.Fatalf("departed member should be safely absent: found=%v err=%v", found, err)
	}
	if found, err := b.membership(context.Background(), "34567890", "23456789"); err != nil || !found {
		t.Fatalf("fresh full-list member lost: found=%v err=%v", found, err)
	}
}
func TestDurableQueueAndGroupScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "incoming"), 0700); err != nil {
		t.Fatal(err)
	}
	b := &bot{cfg: configuration{GroupIDs: []string{"34567890"}, StateDir: dir}, self: "45678901", cooldown: map[string]time.Time{}}
	message := []byte(`{"post_type":"message","message_type":"group","group_id":34567890,"user_id":12345678,"message_id":987,"time":123,"message":[{"type":"text","data":{"text":"绑定"}}]}`)
	if err := b.record(message); err != nil {
		t.Fatal(err)
	}
	if err := b.record(message); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "incoming", "*.json"))
	if len(files) != 1 {
		t.Fatal("duplicate queue item")
	}
	for _, text := range []string{`{"post_type":"message","message_type":"group","group_id":34567890,"user_id":12345678,"time":124,"raw_message":"普通聊天"}`, `{"post_type":"message","message_type":"group","group_id":99999999,"user_id":12345678,"time":124,"raw_message":"绑定"}`} {
		if err := b.record([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	files, _ = filepath.Glob(filepath.Join(dir, "incoming", "*.json"))
	if len(files) != 1 {
		t.Fatal("out-of-scope message recorded")
	}
}
