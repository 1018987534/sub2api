package service

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type openAILatencyAttempt struct {
	accountID              int64
	bodyBytes              int
	forwardStart           time.Time
	clientAcquireStart     time.Time
	clientAcquireDone      time.Time
	dnsStart               time.Time
	dnsDone                time.Time
	connectStart           time.Time
	connectDone            time.Time
	tlsHandshakeStart      time.Time
	tlsHandshakeDone       time.Time
	getConn                time.Time
	gotConn                time.Time
	wroteRequest           time.Time
	firstResponseByte      time.Time
	firstSSEEvent          time.Time
	firstSemanticEvent     time.Time
	firstDownstreamFlush   time.Time
	firstTextDeltaEvent    time.Time
	firstTextDeltaFlush    time.Time
	firstSSEEventType      string
	firstSemanticEventType string
	firstTextDeltaBytes    int
	upstreamHost           string
	upstreamRemoteAddr     string
	upstreamRequestID      string
	upstreamCFRay          string
	upstreamVia            string
	upstreamServer         string
	upstreamServerTiming   string
	protocol               string
	status                 int
	reused                 bool
	wasIdle                bool
	wroteRequestError      bool
	preamblePendingLines   int
}

// OpenAILatencyTrace carries low-overhead timings for one Responses request.
// It is intentionally request-scoped so account failover attempts can be
// distinguished without changing the HTTPUpstream interface.
type OpenAILatencyTrace struct {
	mu sync.Mutex

	requestStart time.Time
	bodyBytes    int
	model        string
	stream       bool

	authLatencyMs             int64
	ingressToHandlerLatencyMs int64
	requestBodyReadLatencyMs  int64
	routingLatencyMs          int64
	userSlotLatencyMs         int64
	accountSelectionLatencyMs int64
	accountSlotLatencyMs      int64
	edgeRoutingWaitMs         int64
	edgeRoutingSource         string

	attempt           *openAILatencyAttempt
	completedAttempts []openAILatencyAttempt
	attemptCount      int
	logged            bool
	textDeltaLogged   bool
}

type openAILatencyTraceContextKey struct{}

func NewOpenAILatencyTrace(requestStart time.Time, bodyBytes int, model string, stream bool) *OpenAILatencyTrace {
	if requestStart.IsZero() {
		requestStart = time.Now()
	}
	return &OpenAILatencyTrace{
		requestStart: requestStart,
		bodyBytes:    bodyBytes,
		model:        strings.TrimSpace(model),
		stream:       stream,
	}
}

func WithOpenAILatencyTrace(ctx context.Context, trace *OpenAILatencyTrace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if trace == nil {
		return ctx
	}
	return context.WithValue(ctx, openAILatencyTraceContextKey{}, trace)
}

func OpenAILatencyTraceFromContext(ctx context.Context) *OpenAILatencyTrace {
	if ctx == nil {
		return nil
	}
	trace, _ := ctx.Value(openAILatencyTraceContextKey{}).(*OpenAILatencyTrace)
	return trace
}

func OpenAISlowTraceThreshold(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 0
	}
	if cfg.Gateway.OpenAISlowRequestTraceThresholdMs <= 0 {
		return 0
	}
	return time.Duration(cfg.Gateway.OpenAISlowRequestTraceThresholdMs) * time.Millisecond
}

func (t *OpenAILatencyTrace) MarkAuthLatency(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.authLatencyMs = nonNegativeMillis(d)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) MarkIngressToHandlerLatency(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.ingressToHandlerLatencyMs = nonNegativeMillis(d)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) MarkRequestBodyReadLatency(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.requestBodyReadLatencyMs = nonNegativeMillis(d)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) MarkRoutingLatency(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.routingLatencyMs = nonNegativeMillis(d)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) AddUserSlotLatency(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.userSlotLatencyMs += nonNegativeMillis(d)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) AddAccountSelectionLatency(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.accountSelectionLatencyMs += nonNegativeMillis(d)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) AddAccountSlotLatency(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.accountSlotLatencyMs += nonNegativeMillis(d)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) MarkEdgeRouting(waitMs int64, source string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if waitMs > 0 {
		t.edgeRoutingWaitMs = waitMs
	}
	t.edgeRoutingSource = strings.TrimSpace(source)
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) BeginAttempt(accountID int64, bodyBytes int, startedAt time.Time) {
	if t == nil {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	t.mu.Lock()
	t.attemptCount++
	if t.attempt != nil {
		t.completedAttempts = append(t.completedAttempts, *t.attempt)
	}
	t.attempt = &openAILatencyAttempt{
		accountID:    accountID,
		bodyBytes:    bodyBytes,
		forwardStart: startedAt,
	}
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) markAttempt(fn func(*openAILatencyAttempt)) {
	if t == nil || fn == nil {
		return
	}
	t.mu.Lock()
	if t.attempt != nil {
		fn(t.attempt)
	}
	t.mu.Unlock()
}

func (t *OpenAILatencyTrace) MarkClientAcquireStart(at time.Time) {
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.clientAcquireStart.IsZero() {
			a.clientAcquireStart = at
		}
	})
}

func (t *OpenAILatencyTrace) MarkClientAcquireDone(at time.Time) {
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.clientAcquireDone.IsZero() {
			a.clientAcquireDone = at
		}
	})
}

func (t *OpenAILatencyTrace) MarkResponse(resp *http.Response) {
	if t == nil || resp == nil {
		return
	}
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.protocol == "" {
			a.protocol = strings.TrimSpace(resp.Proto)
		}
		if a.status == 0 {
			a.status = resp.StatusCode
		}
		if a.upstreamRequestID == "" {
			a.upstreamRequestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
		}
		if a.upstreamCFRay == "" {
			a.upstreamCFRay = traceHeaderValue(resp.Header, "cf-ray")
		}
		if a.upstreamVia == "" {
			a.upstreamVia = traceHeaderValue(resp.Header, "via")
		}
		if a.upstreamServer == "" {
			a.upstreamServer = traceHeaderValue(resp.Header, "server")
		}
		if a.upstreamServerTiming == "" {
			a.upstreamServerTiming = traceHeaderValue(resp.Header, "server-timing")
		}
	})
}

func (t *OpenAILatencyTrace) MarkFirstSSEEvent(eventType string) {
	if t == nil {
		return
	}
	now := time.Now()
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.firstSSEEvent.IsZero() {
			a.firstSSEEvent = now
			a.firstSSEEventType = strings.TrimSpace(eventType)
		}
	})
}

func (t *OpenAILatencyTrace) MarkFirstSemanticEvent(eventType string) {
	if t == nil {
		return
	}
	now := time.Now()
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.firstSemanticEvent.IsZero() {
			a.firstSemanticEvent = now
			a.firstSemanticEventType = strings.TrimSpace(eventType)
		}
	})
}

func (t *OpenAILatencyTrace) MarkFirstTextDelta(data []byte, eventType string) bool {
	if t == nil || strings.TrimSpace(eventType) != "response.output_text.delta" {
		return false
	}
	delta := gjson.GetBytes(data, "delta")
	if delta.Type != gjson.String || delta.String() == "" {
		return false
	}
	now := time.Now()
	first := false
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.firstTextDeltaEvent.IsZero() {
			a.firstTextDeltaEvent = now
			a.firstTextDeltaBytes = len(delta.String())
			first = true
		}
	})
	return first
}

func (t *OpenAILatencyTrace) MarkPreamblePendingLines(lines int) {
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.preamblePendingLines == 0 && lines > 0 {
			a.preamblePendingLines = lines
		}
	})
}

func (t *OpenAILatencyTrace) MarkFirstDownstreamFlush() bool {
	if t == nil {
		return false
	}
	now := time.Now()
	first := false
	t.markAttempt(func(a *openAILatencyAttempt) {
		if a.firstDownstreamFlush.IsZero() {
			a.firstDownstreamFlush = now
			first = true
		}
	})
	return first
}

func (t *OpenAILatencyTrace) MarkFirstTextDeltaFlush() bool {
	if t == nil {
		return false
	}
	now := time.Now()
	first := false
	t.markAttempt(func(a *openAILatencyAttempt) {
		if !a.firstTextDeltaEvent.IsZero() && a.firstTextDeltaFlush.IsZero() {
			a.firstTextDeltaFlush = now
			first = true
		}
	})
	return first
}

func (t *OpenAILatencyTrace) LogIfSlow(ctx context.Context, threshold time.Duration, stage string, accountID int64, upstreamRequestID string) {
	if t == nil || threshold <= 0 {
		return
	}
	now := time.Now()
	t.mu.Lock()
	textDeltaStage := strings.TrimSpace(stage) == "first_text_delta_flush"
	observedAt := now
	if t.attempt != nil {
		if textDeltaStage && !t.attempt.firstTextDeltaFlush.IsZero() {
			observedAt = t.attempt.firstTextDeltaFlush
		} else if !textDeltaStage && !t.attempt.firstDownstreamFlush.IsZero() {
			observedAt = t.attempt.firstDownstreamFlush
		}
	}
	alreadyLogged := t.logged
	if textDeltaStage {
		alreadyLogged = t.textDeltaLogged
	}
	if alreadyLogged || t.requestStart.IsZero() || observedAt.Sub(t.requestStart) < threshold {
		t.mu.Unlock()
		return
	}
	if textDeltaStage {
		t.textDeltaLogged = true
	} else {
		t.logged = true
	}
	requestStart := t.requestStart
	bodyBytes := t.bodyBytes
	model := t.model
	stream := t.stream
	authLatencyMs := t.authLatencyMs
	ingressToHandlerLatencyMs := t.ingressToHandlerLatencyMs
	requestBodyReadLatencyMs := t.requestBodyReadLatencyMs
	routingLatencyMs := t.routingLatencyMs
	userSlotLatencyMs := t.userSlotLatencyMs
	accountSelectionLatencyMs := t.accountSelectionLatencyMs
	accountSlotLatencyMs := t.accountSlotLatencyMs
	edgeRoutingWaitMs := t.edgeRoutingWaitMs
	edgeRoutingSource := t.edgeRoutingSource
	attemptCount := t.attemptCount
	var attempt openAILatencyAttempt
	attempts := make([]openAILatencyAttempt, 0, len(t.completedAttempts)+1)
	attempts = append(attempts, t.completedAttempts...)
	if t.attempt != nil {
		attempt = *t.attempt
		attempts = append(attempts, attempt)
	}
	t.mu.Unlock()

	if accountID == 0 {
		accountID = attempt.accountID
	}
	if strings.TrimSpace(upstreamRequestID) == "" {
		upstreamRequestID = attempt.upstreamRequestID
	}
	firstFlush := attempt.firstDownstreamFlush
	if firstFlush.IsZero() {
		firstFlush = now
	}
	requestPrepareMs, clientAcquireMs, transportDispatchMs := int64(0), int64(0), int64(0)
	dnsMs, connectMs, tlsHandshakeMs := int64(0), int64(0), int64(0)
	getConnWaitMs, requestWriteMs, responseHeaderWaitMs := int64(0), int64(0), int64(0)
	firstSSEAfterHeaderMs, firstSemanticAfterSSEMs, firstFlushAfterSemanticMs := int64(0), int64(0), int64(0)
	firstTextDeltaAfterSemanticMs, firstTextDeltaFlushMs := int64(0), int64(0)
	for _, candidate := range attempts {
		requestPrepareMs += nonNegativeMillis(candidate.clientAcquireStart.Sub(candidate.forwardStart))
		clientAcquireMs += nonNegativeMillis(candidate.clientAcquireDone.Sub(candidate.clientAcquireStart))
		dnsMs += nonNegativeMillis(candidate.dnsDone.Sub(candidate.dnsStart))
		connectMs += nonNegativeMillis(candidate.connectDone.Sub(candidate.connectStart))
		tlsHandshakeMs += nonNegativeMillis(candidate.tlsHandshakeDone.Sub(candidate.tlsHandshakeStart))
		transportDispatchMs += nonNegativeMillis(candidate.getConn.Sub(candidate.clientAcquireDone))
		getConnWaitMs += nonNegativeMillis(candidate.gotConn.Sub(candidate.getConn))
		requestWriteMs += nonNegativeMillis(candidate.wroteRequest.Sub(candidate.gotConn))
		responseHeaderWaitMs += nonNegativeMillis(candidate.firstResponseByte.Sub(candidate.wroteRequest))
		firstSSEAfterHeaderMs += nonNegativeMillis(candidate.firstSSEEvent.Sub(candidate.firstResponseByte))
		firstSemanticAfterSSEMs += nonNegativeMillis(candidate.firstSemanticEvent.Sub(candidate.firstSSEEvent))
		firstFlushAfterSemanticMs += nonNegativeMillis(candidate.firstDownstreamFlush.Sub(candidate.firstSemanticEvent))
		firstTextDeltaAfterSemanticMs += nonNegativeMillis(candidate.firstTextDeltaEvent.Sub(candidate.firstSemanticEvent))
		firstTextDeltaFlushMs += nonNegativeMillis(candidate.firstTextDeltaFlush.Sub(candidate.firstTextDeltaEvent))
	}
	preRoutingOtherMs := authLatencyMs - ingressToHandlerLatencyMs - requestBodyReadLatencyMs
	if preRoutingOtherMs < 0 {
		preRoutingOtherMs = 0
	}
	routingOtherMs := routingLatencyMs - userSlotLatencyMs - accountSelectionLatencyMs - accountSlotLatencyMs
	if routingOtherMs < 0 {
		routingOtherMs = 0
	}
	largestPhase, largestPhaseMs := openAILatencyLargestPhase([]openAILatencyPhase{
		{name: "inbound_request_body_read", ms: requestBodyReadLatencyMs},
		{name: "ingress_auth_middleware", ms: ingressToHandlerLatencyMs},
		{name: "pre_routing_other", ms: preRoutingOtherMs},
		{name: "user_slot_wait", ms: userSlotLatencyMs},
		{name: "account_selection", ms: accountSelectionLatencyMs},
		{name: "account_slot_wait", ms: accountSlotLatencyMs},
		{name: "routing_other", ms: routingOtherMs},
		{name: "upstream_request_prepare", ms: requestPrepareMs},
		{name: "http_client_acquire", ms: clientAcquireMs},
		{name: "transport_dispatch", ms: transportDispatchMs},
		{name: "transport_get_conn_or_h2_slot_wait", ms: getConnWaitMs},
		{name: "request_body_upload", ms: requestWriteMs},
		{name: "upstream_response_header_wait", ms: responseHeaderWaitMs},
		{name: "upstream_first_sse_wait", ms: firstSSEAfterHeaderMs},
		{name: "upstream_preamble_to_semantic_wait", ms: firstSemanticAfterSSEMs},
		{name: "downstream_flush_delay", ms: firstFlushAfterSemanticMs},
		{name: "upstream_semantic_to_text_delta_wait", ms: firstTextDeltaAfterSemanticMs},
		{name: "downstream_text_delta_flush_delay", ms: firstTextDeltaFlushMs},
	})
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway.latency_trace"),
		zap.String("stage", strings.TrimSpace(stage)),
		zap.String("model", model),
		zap.Bool("stream", stream),
		zap.Int("body_bytes", bodyBytes),
		zap.Int64("account_id", accountID),
		zap.Int("attempt_count", attemptCount),
		zap.Int64("request_elapsed_ms", nonNegativeMillis(firstFlush.Sub(requestStart))),
		zap.Int64("auth_ms", authLatencyMs),
		zap.Int64("ingress_auth_middleware_ms", ingressToHandlerLatencyMs),
		zap.Int64("inbound_body_read_ms", requestBodyReadLatencyMs),
		zap.Int64("pre_routing_other_ms", preRoutingOtherMs),
		zap.Int64("routing_ms", routingLatencyMs),
		zap.Int64("user_slot_ms", userSlotLatencyMs),
		zap.Int64("account_selection_ms", accountSelectionLatencyMs),
		zap.Int64("account_slot_ms", accountSlotLatencyMs),
		zap.Int64("edge_routing_wait_ms", edgeRoutingWaitMs),
		zap.String("edge_routing_source", edgeRoutingSource),
		zap.Int64("routing_other_ms", routingOtherMs),
		zap.Int64("forward_to_flush_ms", nonNegativeMillis(firstFlush.Sub(attempt.forwardStart))),
		zap.Int64("upstream_request_prepare_ms", requestPrepareMs),
		zap.Int64("client_acquire_ms", clientAcquireMs),
		zap.Int64("transport_dispatch_ms", transportDispatchMs),
		zap.Int64("dns_lookup_ms", dnsMs),
		zap.Int64("tcp_connect_ms", connectMs),
		zap.Int64("tls_handshake_ms", tlsHandshakeMs),
		zap.Int64("get_conn_wait_ms", getConnWaitMs),
		zap.Int64("forward_to_got_conn_ms", nonNegativeMillis(attempt.gotConn.Sub(attempt.forwardStart))),
		zap.Int64("request_write_after_conn_ms", requestWriteMs),
		zap.Int64("response_header_wait_ms", responseHeaderWaitMs),
		zap.Int64("first_sse_after_header_ms", firstSSEAfterHeaderMs),
		zap.Int64("first_semantic_after_sse_ms", firstSemanticAfterSSEMs),
		zap.Int64("first_flush_after_semantic_ms", firstFlushAfterSemanticMs),
		zap.Int64("first_text_delta_after_semantic_ms", firstTextDeltaAfterSemanticMs),
		zap.Int64("first_text_delta_flush_ms", firstTextDeltaFlushMs),
		zap.Int64("request_to_first_text_delta_ms", nonNegativeMillis(attempt.firstTextDeltaEvent.Sub(requestStart))),
		zap.Int64("request_to_first_text_flush_ms", nonNegativeMillis(attempt.firstTextDeltaFlush.Sub(requestStart))),
		zap.Int("first_text_delta_bytes", attempt.firstTextDeltaBytes),
		zap.String("largest_phase", largestPhase),
		zap.Int64("largest_phase_ms", largestPhaseMs),
		zap.Int("preamble_pending_lines", attempt.preamblePendingLines),
		zap.Int64("threshold_ms", threshold.Milliseconds()),
		zap.Int64("account_attempt_body_bytes", int64(attempt.bodyBytes)),
		zap.Int64("account_id_from_attempt", attempt.accountID),
		zap.Int("status_code", attempt.status),
		zap.Bool("conn_reused", attempt.reused),
		zap.Bool("conn_was_idle", attempt.wasIdle),
		zap.Bool("request_write_error", attempt.wroteRequestError),
		zap.String("protocol", attempt.protocol),
		zap.String("upstream_host", attempt.upstreamHost),
		zap.String("upstream_remote_addr", attempt.upstreamRemoteAddr),
		zap.String("upstream_cf_ray", attempt.upstreamCFRay),
		zap.String("upstream_via", attempt.upstreamVia),
		zap.String("upstream_server", attempt.upstreamServer),
		zap.String("upstream_server_timing", attempt.upstreamServerTiming),
		zap.String("first_sse_event_type", attempt.firstSSEEventType),
		zap.String("first_semantic_event_type", attempt.firstSemanticEventType),
		zap.String("upstream_request_id", strings.TrimSpace(upstreamRequestID)),
	}
	if requestID, _ := ctx.Value(ctxkey.RequestID).(string); strings.TrimSpace(requestID) != "" {
		fields = append(fields, zap.String("request_id", strings.TrimSpace(requestID)))
	}
	if clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string); strings.TrimSpace(clientRequestID) != "" {
		fields = append(fields, zap.String("client_request_id", strings.TrimSpace(clientRequestID)))
	}
	zapLogger := logger.FromContext(ctx).With(fields...)
	zapLogger.Warn("openai.slow_stream_latency")
}

type openAILatencyPhase struct {
	name string
	ms   int64
}

func openAILatencyLargestPhase(phases []openAILatencyPhase) (string, int64) {
	largest := openAILatencyPhase{name: "unclassified"}
	for _, phase := range phases {
		if phase.ms > largest.ms {
			largest = phase
		}
	}
	return largest.name, largest.ms
}

func nonNegativeMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

const maxLatencyTraceHeaderValue = 256

func traceHeaderValue(headers http.Header, key string) string {
	value := strings.TrimSpace(headers.Get(key))
	if len(value) > maxLatencyTraceHeaderValue {
		return value[:maxLatencyTraceHeaderValue]
	}
	return value
}

// WithOpenAIHTTPTrace attaches callbacks to the request reaching net/http.
// Callers must invoke this immediately before Client.Do so transport callbacks
// cover connection-pool and HTTP/2 stream-slot waits.
func WithOpenAIHTTPTrace(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	trace := OpenAILatencyTraceFromContext(req.Context())
	if trace == nil {
		return req
	}
	clientTrace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.dnsStart.IsZero() {
					a.dnsStart = now
				}
			})
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.dnsDone.IsZero() {
					a.dnsDone = now
				}
			})
		},
		ConnectStart: func(_, _ string) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.connectStart.IsZero() {
					a.connectStart = now
				}
			})
		},
		ConnectDone: func(_, _ string, err error) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.connectDone.IsZero() || err == nil {
					a.connectDone = now
				}
			})
		},
		TLSHandshakeStart: func() {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.tlsHandshakeStart.IsZero() {
					a.tlsHandshakeStart = now
				}
			})
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.tlsHandshakeDone.IsZero() {
					a.tlsHandshakeDone = now
				}
			})
		},
		GetConn: func(hostPort string) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.getConn.IsZero() {
					a.getConn = now
					a.upstreamHost = hostPort
				}
			})
		},
		GotConn: func(info httptrace.GotConnInfo) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.gotConn.IsZero() {
					a.gotConn = now
					a.reused = info.Reused
					a.wasIdle = info.WasIdle
					if info.Conn != nil && info.Conn.RemoteAddr() != nil {
						a.upstreamRemoteAddr = info.Conn.RemoteAddr().String()
					}
				}
			})
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.wroteRequest.IsZero() {
					a.wroteRequest = now
					a.wroteRequestError = info.Err != nil
				}
			})
		},
		GotFirstResponseByte: func() {
			now := time.Now()
			trace.markAttempt(func(a *openAILatencyAttempt) {
				if a.firstResponseByte.IsZero() {
					a.firstResponseByte = now
				}
			})
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), clientTrace))
}
