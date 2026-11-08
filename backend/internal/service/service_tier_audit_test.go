package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestServiceTierAuditCorrelatesResponseAndBilling(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core).With(zap.String("request_id", "local-tier-audit")))
	ctx = context.WithValue(ctx, ctxkey.RequestID, "local-tier-audit")
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "client-tier-audit")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	response := beginUpstreamResponseModelObservation(c)
	response.ObserveOpenAI([]byte(`{"response":{"model":"gpt-6.1-sol","service_tier":"priority"}}`), "response.created")
	response.ObserveOpenAI([]byte(`{"response":{"model":"gpt-6.1-sol","service_tier":"default","output":[{"text":"PRIVATE_RESPONSE_CONTENT"}]}}`), "response.completed")
	account := &Account{ID: 12469, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	requestTier := "priority"
	result := &OpenAIForwardResult{RequestID: "upstream-audit-id", ServiceTier: &requestTier, UpstreamResponseServiceTier: response.ServiceTier()}
	result.UpstreamHeaders = http.Header{"X-Service-Tier": {"priority"}, "Authorization": {"PRIVATE_RESPONSE_CONTENT"}}
	svc := &OpenAIGatewayService{}
	ctx = withOpenAIFastPolicyContext(ctx, &OpenAIFastPolicySettings{BillingFollowUpstreamTier: true})
	resolution, err := svc.resolveServiceTierBilling(ctx, account, result)
	require.NoError(t, err)
	require.Equal(t, "default", resolution.Billing)
	require.Equal(t, "default", *result.ServiceTier)
	entries := logs.All()
	require.Len(t, entries, 3)
	for _, entry := range entries {
		require.Equal(t, "local-tier-audit", entry.ContextMap()["request_id"])
		encoded, encodeErr := json.Marshal(entry.ContextMap())
		require.NoError(t, encodeErr)
		require.NotContains(t, string(encoded), "PRIVATE_RESPONSE_CONTENT")
	}
	require.Equal(t, "priority", entries[0].ContextMap()["raw_tier"])
	require.Equal(t, "non_terminal_echo", entries[0].ContextMap()["ignored_reason"])
	require.Equal(t, "default", entries[1].ContextMap()["raw_tier"])
	require.Equal(t, "billing.service_tier_resolution", entries[2].Message)
	require.Equal(t, "priority", entries[2].ContextMap()["requested_tier"])
	require.Equal(t, "default", entries[2].ContextMap()["observed_tier"])
	require.Equal(t, "client-tier-audit", entries[2].ContextMap()["client_request_id"])
	require.Equal(t, map[string]string{"X-Service-Tier": "priority"}, entries[2].ContextMap()["header_tiers"])
}

func TestServiceTierAuditExposesModelMissingWithoutChangingBilling(t *testing.T) {
	var metadata []upstreamServiceTierAudit
	o := &upstreamResponseModelObserver{tierAudit: func(value upstreamServiceTierAudit) { metadata = append(metadata, value) }}
	o.ObserveOpenAI([]byte(`{"response":{"service_tier":"priority"}}`), "response.completed")
	require.Empty(t, o.ServiceTier())
	require.Len(t, metadata, 1)
	require.Equal(t, "priority", metadata[0].RawTier)
	require.Equal(t, "model_missing", metadata[0].IgnoredReason)
}

func TestServiceTierAuditBoundsUntrustedFieldsAndSkipsDeltas(t *testing.T) {
	var metadata []upstreamServiceTierAudit
	o := &upstreamResponseModelObserver{tierAudit: func(value upstreamServiceTierAudit) { metadata = append(metadata, value) }}
	o.ObserveOpenAI([]byte(`{"model":"gpt-6.1-sol","delta":"PRIVATE_DELTA","service_tier":"priority"}`), "response.output_text.delta")
	o.ObserveOpenAI([]byte(`{"model":"gpt-6.1-sol","service_tier":"PRIVATE_UNKNOWN_TIER"}`), "response.completed")
	require.Len(t, metadata, 1)
	require.Equal(t, "unknown", metadata[0].RawTier)
	require.Equal(t, "tier_unrecognized", metadata[0].IgnoredReason)
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PRIVATE_")
}
