package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/qqactivity"
)

func TestPrivateQueueEncryptedOrderedAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "incoming"), 0700); err != nil {
		t.Fatal(err)
	}
	var received []qqactivity.PrivateEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/get_group_member_info":
			fmt.Fprint(w, `{"status":"ok","data":{"user_id":12345678}}`)
		case "/get_stranger_info":
			fmt.Fprint(w, `{"status":"ok","data":{"qqLevel":21}}`)
		case "/bridge":
			var p struct {
				Action  string                  `json:"action"`
				Private qqactivity.PrivateEvent `json:"private"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}
			if p.Action != "private" {
				t.Errorf("unexpected bridge action: %s", p.Action)
			}
			received = append(received, p.Private)
			fmt.Fprint(w, `{"code":0}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	b := &bot{cfg: configuration{StateDir: dir, GroupIDs: []string{"34567890"}, HTTPURL: server.URL, BridgeURL: server.URL + "/bridge"}, self: "99999999", bridgeToken: strings.Repeat("k", 48), client: server.Client()}
	private := func(id int, text string) []byte {
		return []byte(fmt.Sprintf(`{"post_type":"message","message_type":"private","user_id":12345678,"message_id":%d,"time":%d,"raw_message":%q}`, id, time.Now().Unix(), text))
	}
	raw := private(100, "654321")
	if err := b.record(raw); err != nil {
		t.Fatal(err)
	}
	if err := b.record(raw); err != nil {
		t.Fatal(err)
	}
	if err := b.record(private(101, "确认更换")); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "incoming", "*.private"))
	plain, _ := filepath.Glob(filepath.Join(dir, "incoming", "*.json"))
	if len(files) != 2 || len(plain) != 0 {
		t.Fatal("private events leaked into group spool or duplicated")
	}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("654321")) || bytes.Contains(data, []byte("确认更换")) || bytes.Contains(data, []byte("raw_message")) {
			t.Fatal("plaintext private input persisted")
		}
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0600 {
			t.Fatal("private queue not permission restricted")
		}
		if err = b.consume(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 2 || received[0].Text != "654321" || received[1].Text != "确认更换" || received[0].QQ != "12345678" || !received[0].MemberConfirmed {
		t.Fatalf("private input reordered or identity lost: count=%d", len(received))
	}
	firstID := received[0].ID
	if err := b.record(raw); err != nil {
		t.Fatal(err)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "incoming", "*.private"))
	if err := b.consume(context.Background(), files[0]); err != nil {
		t.Fatal(err)
	}
	if received[2].ID != firstID {
		t.Fatal("replayed private message changed backend idempotency key")
	}
	if err := b.record(private(102, "123456")); err != nil {
		t.Fatal(err)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "incoming", "*.private"))
	data, _ := os.ReadFile(files[0])
	data[len(data)-1] ^= 1
	if err := os.WriteFile(files[0], data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.consume(context.Background(), files[0]); err == nil {
		t.Fatal("tampered private ciphertext accepted")
	}
}

func TestFriendRequestsApprovedBeforeGreeting(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("approval_failure=", fail), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "incoming"), 0700); err != nil {
				t.Fatal(err)
			}
			friend := false
			approved, greetings := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var p map[string]any
				json.NewDecoder(r.Body).Decode(&p)
				switch r.URL.Path {
				case "/get_friend_list":
					if friend {
						fmt.Fprint(w, `{"status":"ok","data":[{"user_id":12345678}]}`)
					} else {
						fmt.Fprint(w, `{"status":"ok","data":[]}`)
					}
				case "/set_friend_add_request":
					if p["flag"] != "opaque-request-flag" || p["approve"] != true {
						t.Error("wrong approval request")
					}
					approved++
					if fail {
						fmt.Fprint(w, `{"status":"failed","retcode":1200}`)
						return
					}
					friend = true
					fmt.Fprint(w, `{"status":"ok"}`)
				case "/bridge":
					if !friend || p["action"] != "friend" || p["qq"] != "12345678" {
						t.Error("greeting before confirmed friendship")
					}
					greetings++
					fmt.Fprint(w, `{"code":0}`)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			}))
			defer server.Close()
			b := &bot{cfg: configuration{StateDir: dir, HTTPURL: server.URL, BridgeURL: server.URL + "/bridge"}, client: server.Client()}
			raw := []byte(fmt.Sprintf(`{"post_type":"request","request_type":"friend","user_id":12345678,"time":%d,"flag":"opaque-request-flag"}`, time.Now().Unix()))
			if err := b.record(raw); err != nil {
				t.Fatal(err)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "incoming", "*.json"))
			if len(files) != 1 {
				t.Fatal("friend request discarded")
			}
			err := b.consume(context.Background(), files[0])
			if fail {
				if err == nil || greetings != 0 {
					t.Fatal("failed request greeted or acknowledged")
				}
				return
			}
			if err != nil || approved != 1 || greetings != 1 {
				t.Fatalf("approval flow: %d %d %v", approved, greetings, err)
			}
			if err = b.record(raw); err != nil {
				t.Fatal(err)
			}
			files, _ = filepath.Glob(filepath.Join(dir, "incoming", "*.json"))
			if err = b.consume(context.Background(), files[0]); err != nil {
				t.Fatal(err)
			}
			if approved != 1 {
				t.Fatal("duplicate friend request approved twice")
			}
		})
	}
}

func TestPrivateOutboxNeverUsesGroupTransport(t *testing.T) {
	var delivered bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		switch r.URL.Path {
		case "/bridge":
			if p["action"] == "outbox" {
				fmt.Fprint(w, `{"code":0,"data":[{"id":1,"scope":"private","qq":"12345678","text":"验证码已发送，请在私信填写。"}]}`)
			} else if p["action"] == "ack" {
				delivered = p["delivered"] == true
				fmt.Fprint(w, `{"code":0}`)
			} else {
				t.Error("unexpected action")
			}
		case "/send_private_msg":
			if p["user_id"] != "12345678" || p["group_id"] != nil {
				t.Error("incorrect private recipient")
			}
			fmt.Fprint(w, `{"status":"ok","data":{"message_id":123}}`)
		default:
			t.Errorf("private message reached group endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	b := &bot{cfg: configuration{HTTPURL: server.URL, BridgeURL: server.URL + "/bridge", StateDir: t.TempDir()}, client: server.Client()}
	if err := b.outputs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("private receipt not acknowledged")
	}
}

func TestNewMemberWelcomeUsesActualMention(t *testing.T) {
	var segments []struct {
		Type string            `json:"type"`
		Data map[string]string `json:"data"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bridge":
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			if p["action"] == "outbox" {
				fmt.Fprintf(w, `{"code":0,"data":[{"id":1,"scope":"group","group_id":"34567890","qq":"12345678","text":%q}]}`, qqactivity.Prompt("M01", nil))
			} else {
				fmt.Fprint(w, `{"code":0}`)
			}
		case "/get_group_member_info":
			fmt.Fprint(w, `{"status":"ok","data":{"user_id":12345678}}`)
		case "/send_group_msg":
			var p struct {
				Message json.RawMessage `json:"message"`
			}
			json.NewDecoder(r.Body).Decode(&p)
			json.Unmarshal(p.Message, &segments)
			fmt.Fprint(w, `{"status":"ok","data":{"message_id":123}}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	b := &bot{cfg: configuration{HTTPURL: server.URL, BridgeURL: server.URL + "/bridge", GroupIDs: []string{"34567890"}}, client: server.Client()}
	if err := b.outputs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 || segments[0].Data["text"] != "欢迎入群 " || segments[1].Type != "at" || segments[1].Data["qq"] != "12345678" || segments[2].Data["text"] != "，私信管理员 Nono 输入【绑定】来绑定账号，领取 2 刀体验余额试用。" {
		t.Fatal("welcome does not match requested copy or uses a fake mention")
	}
}

func TestPrivateConfirmedReceiptSurvivesAckFailure(t *testing.T) {
	sends, acks := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/send_private_msg" {
			sends++
			fmt.Fprint(w, `{"status":"ok","data":{"message_id":100}}`)
			return
		}
		if r.URL.Path == "/bridge" {
			acks++
			if acks == 1 {
				fmt.Fprint(w, `{"code":1}`)
			} else {
				fmt.Fprint(w, `{"code":0}`)
			}
			return
		}
		t.Errorf("unexpected endpoint %s", r.URL.Path)
	}))
	defer server.Close()
	cfg := configuration{HTTPURL: server.URL, BridgeURL: server.URL + "/bridge", StateDir: t.TempDir()}
	b := &bot{cfg: cfg, client: server.Client()}
	m := qqactivity.Outbox{ID: 10, Scope: "private", QQ: "12345678", Text: "你好，发送【绑定】开始绑定官网账号。"}
	if err := b.outputPrivate(context.Background(), m); err == nil {
		t.Fatal("ack failure ignored")
	}
	b = &bot{cfg: cfg, client: server.Client()}
	if err := b.outputPrivate(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || acks != 2 {
		t.Fatalf("confirmed receipt resent: sends=%d acks=%d", sends, acks)
	}
}
