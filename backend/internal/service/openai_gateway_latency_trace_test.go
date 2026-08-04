package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestOpenAILatencyTraceLogsSlowFirstFlushOnce(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	requestStart := time.Now().Add(-10 * time.Second)
	trace := NewOpenAILatencyTrace(requestStart, 4*1024*1024, "gpt-test", true)
	trace.authLatencyMs = 20
	trace.userSlotLatencyMs = 30
	trace.accountSelectionLatencyMs = 40
	trace.accountSlotLatencyMs = 50
	trace.attemptCount = 1
	trace.attempt = &openAILatencyAttempt{
		accountID:              12017,
		bodyBytes:              4 * 1024 * 1024,
		forwardStart:           requestStart.Add(100 * time.Millisecond),
		clientAcquireStart:     requestStart.Add(110 * time.Millisecond),
		clientAcquireDone:      requestStart.Add(120 * time.Millisecond),
		getConn:                requestStart.Add(120 * time.Millisecond),
		gotConn:                requestStart.Add(3120 * time.Millisecond),
		wroteRequest:           requestStart.Add(3500 * time.Millisecond),
		firstResponseByte:      requestStart.Add(4 * time.Second),
		firstSSEEvent:          requestStart.Add(4100 * time.Millisecond),
		firstSemanticEvent:     requestStart.Add(5900 * time.Millisecond),
		firstDownstreamFlush:   requestStart.Add(6 * time.Second),
		firstSSEEventType:      "response.created",
		firstSemanticEventType: "response.output_text.delta",
		upstreamHost:           "example.test:443",
		upstreamRequestID:      "rid-upstream",
		protocol:               "HTTP/2.0",
		status:                 http.StatusOK,
		reused:                 true,
		preamblePendingLines:   6,
	}
	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "rid-local")

	trace.LogIfSlow(ctx, 3*time.Second, "first_downstream_flush", 0, "")
	trace.LogIfSlow(ctx, 3*time.Second, "handler_end", 0, "")

	require.True(t, logSink.ContainsMessageAtLevel("openai.slow_stream_latency", "warn"))
	require.True(t, logSink.ContainsFieldValue("largest_phase", "transport_get_conn_or_h2_slot_wait"))
	require.True(t, logSink.ContainsFieldValue("largest_phase_ms", "3000"))
	require.True(t, logSink.ContainsFieldValue("request_elapsed_ms", "6000"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_id", "rid-upstream"))
	require.True(t, logSink.ContainsFieldValue("request_id", "rid-local"))

	logSink.mu.Lock()
	require.Len(t, logSink.events, 1)
	logSink.mu.Unlock()
}

func TestOpenAILatencyTraceDoesNotLogLongCompletionWhenFirstFlushWasFast(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	requestStart := time.Now().Add(-10 * time.Second)
	trace := NewOpenAILatencyTrace(requestStart, 1024, "gpt-test", true)
	trace.attemptCount = 1
	trace.attempt = &openAILatencyAttempt{
		forwardStart:         requestStart.Add(50 * time.Millisecond),
		firstDownstreamFlush: requestStart.Add(900 * time.Millisecond),
	}

	trace.LogIfSlow(context.Background(), 3*time.Second, "stream_end", 0, "")

	require.False(t, logSink.ContainsMessage("openai.slow_stream_latency"))
}

func TestOpenAILatencyTraceAggregatesFailoverTransportPhases(t *testing.T) {
	trace := NewOpenAILatencyTrace(time.Now().Add(-8*time.Second), 128, "gpt-test", true)
	firstStart := trace.requestStart.Add(100 * time.Millisecond)
	trace.BeginAttempt(1, 128, firstStart)
	trace.markAttempt(func(a *openAILatencyAttempt) {
		a.getConn = firstStart
		a.gotConn = firstStart.Add(1200 * time.Millisecond)
		a.wroteRequest = firstStart.Add(1300 * time.Millisecond)
	})
	secondStart := firstStart.Add(1500 * time.Millisecond)
	trace.BeginAttempt(2, 128, secondStart)
	trace.markAttempt(func(a *openAILatencyAttempt) {
		a.getConn = secondStart
		a.gotConn = secondStart.Add(800 * time.Millisecond)
	})

	trace.mu.Lock()
	completed := append([]openAILatencyAttempt(nil), trace.completedAttempts...)
	current := *trace.attempt
	trace.mu.Unlock()
	require.Len(t, completed, 1)
	require.Equal(t, int64(2000), nonNegativeMillis(completed[0].gotConn.Sub(completed[0].getConn))+nonNegativeMillis(current.gotConn.Sub(current.getConn)))
}

func TestWithOpenAIHTTPTraceRecordsNetHTTPPhases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-request-id", "rid-httptrace")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	trace := NewOpenAILatencyTrace(time.Now(), 2, "gpt-test", true)
	trace.BeginAttempt(42, 2, time.Now())
	ctx := WithOpenAILatencyTrace(context.Background(), trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(WithOpenAIHTTPTrace(req))
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	trace.MarkResponse(resp)

	trace.mu.Lock()
	attempt := *trace.attempt
	trace.mu.Unlock()
	require.False(t, attempt.getConn.IsZero())
	require.False(t, attempt.gotConn.IsZero())
	require.False(t, attempt.wroteRequest.IsZero())
	require.False(t, attempt.firstResponseByte.IsZero())
	require.Equal(t, http.StatusOK, attempt.status)
	require.Equal(t, "rid-httptrace", attempt.upstreamRequestID)
}
