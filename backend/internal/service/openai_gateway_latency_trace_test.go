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
	trace.edgeRoutingWaitMs = 180
	trace.edgeRoutingSource = "refresh"
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
		upstreamRemoteAddr:     "203.0.113.10:443",
		upstreamRequestID:      "rid-upstream",
		upstreamCFRay:          "ray-test-LAX",
		upstreamVia:            "1.1 Caddy",
		upstreamServer:         "cloudflare",
		upstreamServerTiming:   "origin;dur=2000",
		protocol:               "HTTP/2.0",
		status:                 http.StatusOK,
		reused:                 true,
		upstreamBodyBytes:      4 * 1024 * 1024,
		upstreamWireBytes:      420000,
		upstreamGzipStreamMs:   12,
		upstreamGzipEnabled:    true,
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
	require.True(t, logSink.ContainsFieldValue("upstream_remote_addr", "203.0.113.10:443"))
	require.True(t, logSink.ContainsFieldValue("upstream_cf_ray", "ray-test-LAX"))
	require.True(t, logSink.ContainsFieldValue("upstream_server_timing", "origin;dur=2000"))
	require.True(t, logSink.ContainsFieldValue("request_id", "rid-local"))
	require.True(t, logSink.ContainsFieldValue("edge_routing_wait_ms", "180"))
	require.True(t, logSink.ContainsFieldValue("edge_routing_source", "refresh"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_gzip", "true"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_body_bytes", "4194304"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_wire_bytes", "420000"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_gzip_stream_ms", "12"))

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

func TestOpenAILatencyTraceLogsSlowFirstTextDeltaAfterFastPreambleFlush(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	requestStart := time.Now().Add(-10 * time.Second)
	trace := NewOpenAILatencyTrace(requestStart, 1024, "gpt-test", true)
	trace.attemptCount = 1
	trace.attempt = &openAILatencyAttempt{
		accountID:            12017,
		forwardStart:         requestStart.Add(50 * time.Millisecond),
		firstResponseByte:    requestStart.Add(500 * time.Millisecond),
		firstSSEEvent:        requestStart.Add(510 * time.Millisecond),
		firstSemanticEvent:   requestStart.Add(800 * time.Millisecond),
		firstDownstreamFlush: requestStart.Add(810 * time.Millisecond),
		firstTextDeltaEvent:  requestStart.Add(5 * time.Second),
		firstTextDeltaFlush:  requestStart.Add(5100 * time.Millisecond),
		firstTextDeltaBytes:  4,
	}

	trace.LogIfSlow(context.Background(), 3*time.Second, "first_downstream_flush", 0, "")
	trace.LogIfSlow(context.Background(), 3*time.Second, "first_text_delta_flush", 0, "")

	require.True(t, logSink.ContainsMessageAtLevel("openai.slow_stream_latency", "warn"))
	require.True(t, logSink.ContainsFieldValue("stage", "first_text_delta_flush"))
	require.True(t, logSink.ContainsFieldValue("request_to_first_text_delta_ms", "5000"))
	require.True(t, logSink.ContainsFieldValue("request_to_first_text_flush_ms", "5100"))
	require.True(t, logSink.ContainsFieldValue("first_text_delta_after_semantic_ms", "4200"))
	require.True(t, logSink.ContainsFieldValue("first_text_delta_bytes", "4"))

	logSink.mu.Lock()
	require.Len(t, logSink.events, 1)
	logSink.mu.Unlock()
}

func TestOpenAILatencyTraceMarksOnlyNonEmptyOutputTextDelta(t *testing.T) {
	trace := NewOpenAILatencyTrace(time.Now(), 1024, "gpt-test", true)
	trace.BeginAttempt(12017, 1024, time.Now())

	require.False(t, trace.MarkFirstTextDelta([]byte(`{"type":"response.output_item.added"}`), "response.output_item.added"))
	require.False(t, trace.MarkFirstTextDelta([]byte(`{"type":"response.output_text.delta","delta":""}`), "response.output_text.delta"))
	require.True(t, trace.MarkFirstTextDelta([]byte(`{"type":"response.output_text.delta","delta":"text"}`), "response.output_text.delta"))
	require.False(t, trace.MarkFirstTextDelta([]byte(`{"type":"response.output_text.delta","delta":"again"}`), "response.output_text.delta"))
	require.True(t, trace.MarkFirstTextDeltaFlush())
	require.False(t, trace.MarkFirstTextDeltaFlush())
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

func TestOpenAILatencyTraceSeparatesInitialRoutingFromFailedAttempt(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	requestStart := time.Now().Add(-30 * time.Second)
	trace := NewOpenAILatencyTrace(requestStart, 128, "gpt-test", true)
	trace.MarkRoutingLatency(55 * time.Millisecond)
	firstStart := requestStart.Add(100 * time.Millisecond)
	trace.BeginAttempt(12006, 128, firstStart)
	trace.markAttempt(func(a *openAILatencyAttempt) {
		a.wroteRequest = firstStart.Add(20 * time.Millisecond)
		a.firstResponseByte = firstStart.Add(20 * time.Second)
		a.status = http.StatusBadGateway
		a.upstreamHost = "xiaobaishu.test:443"
		a.upstreamRequestID = "rid-failed"
	})
	trace.EndAttempt(firstStart.Add(20*time.Second), true)

	secondStart := firstStart.Add(20*time.Second + 40*time.Millisecond)
	trace.BeginAttempt(12017, 128, secondStart)
	trace.markAttempt(func(a *openAILatencyAttempt) {
		a.wroteRequest = secondStart.Add(20 * time.Millisecond)
		a.firstResponseByte = secondStart.Add(1500 * time.Millisecond)
		a.firstSSEEvent = secondStart.Add(1501 * time.Millisecond)
		a.firstSemanticEvent = secondStart.Add(1800 * time.Millisecond)
		a.firstDownstreamFlush = requestStart.Add(23 * time.Second)
		a.status = http.StatusOK
		a.upstreamHost = "shayulajiao.test:443"
		a.upstreamCFRay = "ray-success"
		a.upstreamRequestID = "rid-success"
	})

	trace.LogIfSlow(context.Background(), 3*time.Second, "first_downstream_flush", 0, "")

	require.True(t, logSink.ContainsFieldValue("routing_ms", "55"))
	require.True(t, logSink.ContainsFieldValue("failed_attempt_elapsed_ms", "20000"))
	require.True(t, logSink.ContainsFieldValue("failover_wait_ms", "40"))
	require.True(t, logSink.ContainsFieldValue("attempt_summaries", "rid-failed"))
	require.True(t, logSink.ContainsFieldValue("attempt_summaries", "rid-success"))
}

func TestWithOpenAIHTTPTraceRecordsNetHTTPPhases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-request-id", "rid-httptrace")
		w.Header().Set("cf-ray", "ray-httptrace-LAX")
		w.Header().Set("via", "1.1 Caddy")
		w.Header().Set("server", "cloudflare")
		w.Header().Set("server-timing", "origin;dur=2000")
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
	require.Equal(t, "ray-httptrace-LAX", attempt.upstreamCFRay)
	require.Equal(t, "1.1 Caddy", attempt.upstreamVia)
	require.Equal(t, "cloudflare", attempt.upstreamServer)
	require.Equal(t, "origin;dur=2000", attempt.upstreamServerTiming)
	require.NotEmpty(t, attempt.upstreamRemoteAddr)
}
