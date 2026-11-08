package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Tier audit records only bounded protocol metadata, never response content.
type upstreamServiceTierAudit struct {
	EventType      string
	RawTier        string
	NormalizedTier string
	IgnoredReason  string
	ModelPresent   bool
	TierPresent    bool
	Terminal       bool
}

func serviceTierAuditValue(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case "", "priority", "fast", "default", "standard", "flex", "ultrafast", "scale", "auto":
		return value
	default:
		return "unknown"
	}
}

func (o *upstreamResponseModelObserver) auditOpenAIServiceTier(payload []byte, eventType string, modelPresent, terminal bool) {
	if o.tierAudit == nil {
		return
	}
	eventType = strings.TrimSpace(eventType)
	if !terminal && eventType != "response.created" && (eventType != "" || o.firstUntypedTierAudited) {
		return
	}
	if !gjson.ValidBytes(payload) {
		return
	}
	if eventType == "" {
		o.firstUntypedTierAudited = true
	}
	rawTier := firstValidTrimmedGJSONString(payload, "response.service_tier", "service_tier")
	metadata := upstreamServiceTierAudit{
		EventType: eventType, RawTier: serviceTierAuditValue(rawTier),
		NormalizedTier: normalizeObservedOpenAIServiceTier(rawTier),
		ModelPresent:   modelPresent, Terminal: terminal,
		TierPresent: gjson.GetBytes(payload, "response.service_tier").Exists() || gjson.GetBytes(payload, "service_tier").Exists(),
	}
	switch {
	case !modelPresent:
		metadata.IgnoredReason = "model_missing"
	case !terminal && eventType != "":
		metadata.IgnoredReason = "non_terminal_echo"
	case !metadata.TierPresent:
		metadata.IgnoredReason = "tier_missing"
	case metadata.NormalizedTier == "":
		metadata.IgnoredReason = "tier_unrecognized"
	}
	o.tierAudit(metadata)
}

func serviceTierAuditLogger(ctx context.Context) func(upstreamServiceTierAudit) {
	l := logger.FromContext(ctx)
	return func(metadata upstreamServiceTierAudit) {
		l.Info("upstream.service_tier_observed",
			zap.String("event_type", metadata.EventType),
			zap.Bool("terminal", metadata.Terminal),
			zap.Bool("model_present", metadata.ModelPresent),
			zap.Bool("tier_present", metadata.TierPresent),
			zap.String("raw_tier", metadata.RawTier),
			zap.String("normalized_tier", metadata.NormalizedTier),
			zap.String("ignored_reason", metadata.IgnoredReason),
		)
	}
}

func logServiceTierBillingResolution(ctx context.Context, account *Account, result *OpenAIForwardResult, resolution ServiceTierBillingResolution) {
	l := logger.FromContext(ctx)
	if requestID, _ := ctx.Value(ctxkey.RequestID).(string); requestID != "" {
		l = l.With(zap.String("request_id", requestID))
	}
	if clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string); clientRequestID != "" {
		l = l.With(zap.String("client_request_id", clientRequestID))
	}
	l.Info("billing.service_tier_resolution",
		zap.Int64("account_id", account.ID),
		zap.String("upstream_request_id", result.RequestID),
		zap.String("requested_tier", serviceTierAuditValue(resolution.Requested)),
		zap.String("observed_tier", serviceTierAuditValue(resolution.Observed)),
		zap.String("billed_tier", serviceTierAuditValue(resolution.Billing)),
		zap.Any("header_tiers", serviceTierAuditHeaders(result.UpstreamHeaders, result.ResponseHeaders)),
		zap.String("policy", "upstream_response"),
	)
}

func serviceTierAuditHeaders(headers ...http.Header) map[string]string {
	values := map[string]string{}
	for _, header := range headers {
		for _, name := range []string{"X-Service-Tier", "OpenAI-Service-Tier", "X-OpenAI-Service-Tier", "X-Sub2API-Service-Tier", "X-Billing-Service-Tier", "X-Upstream-Service-Tier"} {
			if value := strings.TrimSpace(header.Get(name)); value != "" {
				values[name] = serviceTierAuditValue(value)
			}
		}
	}
	return values
}
