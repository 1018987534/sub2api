package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/gin-gonic/gin"
)

type intelligenceRecoveryContextKey struct{}

// RunIntelligenceRecovery bypasses group selection, billing and monitoring
// handlers, but reuses the exact account's authentication, proxy and protocol
// forwarding. There is no fallback to another account and no latency recording.
func (s *OpenAIGatewayService) RunIntelligenceRecovery(parent context.Context, claim *intelligence.RecoveryClaim) intelligence.Record {
	start := time.Now()
	r := intelligence.Record{AccountID: claim.AccountID, CheckedAt: start.UTC(), Status: "error"}
	ctx, cancel := context.WithTimeout(context.WithValue(parent, intelligenceRecoveryContextKey{}, true), time.Duration(claim.Config.TimeoutSeconds)*time.Second)
	defer cancel()
	account, err := s.accountRepo.GetByID(ctx, claim.AccountID)
	if err != nil || account == nil || account.Platform != PlatformOpenAI ||
		account.GetExtraString(intelligencePauseUntilKey) != claim.Generation || !intelligenceIsolationActive(account, start) {
		return r
	}
	// Only the legacy intelligence block may be ignored. Disabled, expired,
	// throttled and unrelated cooldown accounts remain untouched.
	copy := *account
	if strings.HasPrefix(copy.TempUnschedulableReason, "intelligence:") {
		copy.TempUnschedulableUntil = nil
	}
	if !copy.IsSchedulable() {
		return r
	}
	slot, err := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || slot == nil || !slot.Acquired {
		return r
	}
	if slot.ReleaseFunc != nil {
		defer slot.ReleaseFunc()
	}
	path, body, err := intelligence.ProbeRequest(claim.Config)
	if err != nil {
		return r
	}
	writer := &intelligenceRecoveryWriter{header: make(http.Header), cancel: cancel}
	c, _ := gin.CreateTestContext(writer)
	c.Request, err = http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return r
	}
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	switch claim.Config.Protocol {
	case "responses":
		_, err = s.Forward(ctx, c, account, body)
	case "chat_completions":
		_, err = s.ForwardAsChatCompletions(ctx, c, account, body, "", "")
	case "messages":
		_, err = s.ForwardAsAnthropic(ctx, c, account, body, "", "")
	default:
		return r
	}
	r.DurationMS = time.Since(start).Milliseconds()
	if err != nil || writer.overflow || ctx.Err() != nil || writer.status < 200 || writer.status >= 300 {
		return r
	}
	answer, err := intelligence.ProbeAnswer(bytes.NewReader(writer.body.Bytes()), writer.header.Get("Content-Type"), claim.Config.Protocol)
	if err != nil {
		return r
	}
	r.Status = "degraded"
	if intelligence.Matches(answer, claim.Config) {
		r.Status = "normal"
	}
	// Deliberately discard the answer and gateway metrics: internal recovery has
	// no intelligence_check_runs, usage_logs or customer monitor representation.
	return r
}

type intelligenceRecoveryWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
	cancel   context.CancelFunc
}

func (w *intelligenceRecoveryWriter) Header() http.Header { return w.header }
func (w *intelligenceRecoveryWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *intelligenceRecoveryWriter) Flush() {}
func (w *intelligenceRecoveryWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(b) > 2*1024*1024 {
		w.overflow = true
		w.cancel()
		return 0, errors.New("internal recovery response too large")
	}
	return w.body.Write(b)
}
