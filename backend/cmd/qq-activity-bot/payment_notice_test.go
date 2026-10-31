package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPaymentNoticeDelivery(t *testing.T) {
	for _, tc := range []struct {
		name             string
		reward, inGroup  bool
		failFirst        bool
		missingMessageID bool
	}{
		{name: "departed_reward_is_announced", reward: true},
		{name: "present_reward_mentions_recipient", reward: true, inGroup: true},
		{name: "failed_reward_send_retries", reward: true, inGroup: true, failFirst: true},
		{name: "response_without_message_id_stays_pending", reward: true, inGroup: true, missingMessageID: true},
		{name: "departed_ordinary_reply_is_skipped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sends int
			var delivered bool
			var acks []bool
			var sentPayload string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var payload map[string]any
				_ = json.NewDecoder(r.Body).Decode(&payload)
				switch r.URL.Path {
				case "/bridge":
					if payload["action"] == "outbox" {
						if delivered {
							_, _ = fmt.Fprint(w, `{"code":0,"data":[]}`)
						} else {
							_, _ = fmt.Fprintf(w, `{"code":0,"data":[{"id":1,"group_id":"34567890","qq":"12345678","text":"$2 体验额度已到账，可在官网余额中查看。","reward":%t}]}`, tc.reward)
						}
					} else if payload["action"] == "ack" {
						delivered = payload["delivered"] == true
						acks = append(acks, delivered)
						_, _ = fmt.Fprint(w, `{"code":0}`)
					} else {
						t.Errorf("unexpected bridge operation: %v", payload["action"])
					}
				case "/get_group_member_info":
					if tc.inGroup {
						_, _ = fmt.Fprint(w, `{"status":"ok","retcode":0,"data":{"user_id":12345678}}`)
					} else {
						_, _ = fmt.Fprint(w, `{"status":"failed","retcode":1200}`)
					}
				case "/get_group_member_list":
					_, _ = fmt.Fprint(w, `{"status":"ok","retcode":0,"data":[{"user_id":23456789}]}`)
				case "/send_group_msg":
					sends++
					data, _ := json.Marshal(payload)
					sentPayload = string(data)
					if tc.failFirst && sends == 1 {
						_, _ = fmt.Fprint(w, `{"status":"failed","retcode":1200}`)
					} else if tc.missingMessageID {
						_, _ = fmt.Fprint(w, `{"status":"ok","retcode":0,"data":{}}`)
					} else {
						_, _ = fmt.Fprint(w, `{"status":"ok","retcode":0,"data":{"message_id":987654}}`)
					}
				default:
					t.Errorf("unexpected action: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			b := &bot{cfg: configuration{HTTPURL: server.URL, BridgeURL: server.URL + "/bridge", GroupIDs: []string{"34567890"}}, client: server.Client()}
			err := b.outputs(context.Background())
			if tc.failFirst || tc.missingMessageID {
				if err == nil || delivered || len(acks) != 1 || acks[0] {
					t.Fatalf("failed/unconfirmed send marked delivered: err=%v sends=%d ack=%v", err, sends, acks)
				}
				if tc.missingMessageID {
					t.Logf("unconfirmed_send: sends=%d ack=%v", sends, acks)
					return
				}
				err = b.outputs(context.Background())
			}
			wantSends := 0
			if tc.reward || tc.inGroup {
				wantSends = 1
				if tc.failFirst {
					wantSends++
				}
			}
			if err != nil || !delivered || sends != wantSends {
				t.Fatalf("send/ack mismatch: err=%v sends=%d want=%d ack=%v", err, sends, wantSends, acks)
			}
			if tc.reward && !tc.inGroup && (strings.Contains(sentPayload, `"type":"at"`) || strings.Contains(sentPayload, "12345678") || !strings.Contains(sentPayload, "12****78@qq.com")) {
				t.Fatalf("departed reward must announce with masked recipient: %s", sentPayload)
			}
			if tc.reward && tc.inGroup && !strings.Contains(sentPayload, `"type":"at"`) {
				t.Fatal("present recipient was not mentioned")
			}
			if err := b.outputs(context.Background()); err != nil || sends != wantSends {
				t.Fatal("delivered notification sent again")
			}
			t.Logf("sends=%d ack=%v reward=%t member_present=%t", sends, acks, tc.reward, tc.inGroup)
		})
	}
}
