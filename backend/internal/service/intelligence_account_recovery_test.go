package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceRecoveryTargetsPausedAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "chat_completions", "messages"} {
		for _, status := range []string{"normal", "degraded", "error", "cooldown", "stale"} {
			t.Run(protocol+"/"+status, func(t *testing.T) {
				generation := time.Now().Add(20 * time.Minute).Format(time.RFC3339Nano)
				account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-fixture", "base_url": "https://example.com"},
					Extra:       map[string]any{"use_responses_api": true, intelligencePauseUntilKey: generation, intelligenceRecoveryRequiredKey: true}}
				repo := &intelligenceProtectionRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
				body := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"21"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
				if status == "degraded" {
					body = strings.Replace(body, "21", "wrong", 1)
				}
				code := 200
				if status == "error" {
					code = 500
				}
				body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}}
				cfg := &config.Config{}
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, accountRepo: repo}
				template := intelligence.DefaultConfig(79)
				template.Protocol = protocol
				template.Prompt = "fixture question"
				claim := &intelligence.RecoveryClaim{AccountID: 1, Generation: generation, Config: template}
				if status == "cooldown" {
					until := time.Now().Add(time.Hour)
					repo.accounts[0].TempUnschedulableUntil = &until
					repo.accounts[0].TempUnschedulableReason = "unrelated"
				}
				if status == "stale" {
					claim.Generation = "old generation"
				}
				result := svc.RunIntelligenceRecovery(context.Background(), claim)
				want := status
				if status == "cooldown" || status == "stale" {
					want = "error"
				}
				require.Equal(t, want, result.Status)
				require.Empty(t, result.Answer)
				require.Empty(t, result.RequestID)
				if status == "cooldown" || status == "stale" {
					require.Nil(t, upstream.lastReq)
				} else {
					require.NotNil(t, upstream.lastReq)
					require.Equal(t, "Bearer sk-fixture", upstream.lastReq.Header.Get("Authorization"))
					require.Equal(t, "example.com", upstream.lastReq.URL.Host)
					raw, err := io.ReadAll(upstream.lastReq.Body)
					require.NoError(t, err)
					require.Contains(t, string(raw), template.Model)
					require.Contains(t, string(raw), "fixture question")
				}
				require.True(t, intelligenceIsolationActive(&repo.accounts[0], time.Now()))
				t.Logf("fixed_account=1 result=%s public_answer=empty latency_samples=0", result.Status)
			})
		}
	}
}

func TestIntelligenceRecoveryCancellationAndResponseLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), intelligenceRecoveryContextKey{}, true))
	stream, _ := detachStreamUpstreamContext(ctx, true)
	upstream, _ := detachUpstreamContext(ctx)
	cancel()
	require.ErrorIs(t, stream.Err(), context.Canceled)
	require.ErrorIs(t, upstream.Err(), context.Canceled)
	limited, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &intelligenceRecoveryWriter{header: make(http.Header), cancel: cancel}
	_, err := w.Write(make([]byte, 2*1024*1024+1))
	require.Error(t, err)
	require.Zero(t, w.body.Len())
	require.ErrorIs(t, limited.Err(), context.Canceled)
}
