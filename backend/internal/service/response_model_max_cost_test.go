//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayServiceRecordUsage_UpstreamResponseUsesHigherCost(t *testing.T) {
	const luna = "gpt-5.6-luna"
	const terra = "gpt-5.6-terra"
	usage := OpenAIUsage{InputTokens: 4396, CacheReadInputTokens: 3800, OutputTokens: 5}
	for _, tc := range []struct {
		name, sent, response, source, want string
		conflict                           bool
	}{
		{name: "luna_to_terra", sent: luna, response: terra, source: BillingModelSourceUpstream, want: terra},
		{name: "terra_to_luna", sent: terra, response: luna, source: BillingModelSourceUpstream, want: terra},
		{name: "unknown_response", sent: luna, response: "gpt-unpriced", source: BillingModelSourceUpstream, want: luna},
		{name: "conflicting_response", sent: luna, response: terra, source: BillingModelSourceUpstream, want: luna, conflict: true},
		{name: "fixed_request_price", sent: luna, response: terra, source: BillingModelSourceRequested, want: luna},
		{name: "fixed_mapped_price", sent: luna, response: terra, source: BillingModelSourceChannelMapped, want: luna},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			users := &openAIRecordUsageUserRepoStub{}
			svc := newOpenAIRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{}, nil)
			actualWant, err := svc.billingService.CalculateCost(tc.want, usage.billingUsageTokens(), 1.1)
			require.NoError(t, err)
			err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID:                     "response_price_regression_" + tc.name,
					Model:                         tc.sent,
					UpstreamModel:                 tc.sent,
					UpstreamResponseModel:         tc.response,
					UpstreamResponseModelConflict: tc.conflict,
					Usage:                         usage,
					Duration:                      time.Second,
				},
				APIKey:  &APIKey{ID: 1},
				User:    &User{ID: 2},
				Account: &Account{ID: 3, Platform: PlatformOpenAI},
				ChannelUsageFields: ChannelUsageFields{
					ChannelID: 9, OriginalModel: tc.sent, ChannelMappedModel: tc.sent, BillingModelSource: tc.source,
				},
			})
			require.NoError(t, err)
			require.NotNil(t, logs.lastLog)
			require.Equal(t, tc.sent, logs.lastLog.RequestedModel)
			require.NotNil(t, logs.lastLog.UpstreamResponseModel)
			require.Equal(t, tc.response, *logs.lastLog.UpstreamResponseModel)
			t.Logf("sent=%s response=%s token_cost=%.8f debit=%.8f", tc.sent, tc.response, logs.lastLog.TotalCost, users.lastAmount)
			require.InDelta(t, actualWant.TotalCost, logs.lastLog.TotalCost, 1e-12)
			require.InDelta(t, actualWant.ActualCost, logs.lastLog.ActualCost, 1e-12)
			require.InDelta(t, actualWant.ActualCost, users.lastAmount, 1e-12)
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_UpstreamResponseComparesFreeFastAtStandard(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	users := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	const sent, response = "gpt-5.6-luna", "gpt-5.6-terra"
	groupID := int64(107)
	cheap, expensive, sentFast, responseFast := 0.000001, 0.000002, 4.0, 1.5
	group := &Group{
		ID: groupID, Platform: PlatformOpenAI, Status: StatusActive,
		Hydrated: true, RateMultiplier: 0.22, FreeOpenAIFast: true,
		ModelPricing: []ChannelModelPricing{
			{Models: []string{sent}, BillingMode: BillingModeToken, InputPrice: &cheap, FastMultiplier: &sentFast},
			{Models: []string{response}, BillingMode: BillingModeToken, InputPrice: &expensive, FastMultiplier: &responseFast},
		},
	}
	priority := "priority"
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "response_max_free_fast", Model: sent, UpstreamModel: sent, UpstreamResponseModel: response,
			Usage: OpenAIUsage{InputTokens: 100}, ServiceTier: &priority,
		},
		APIKey:  &APIKey{ID: 1, Group: group, GroupID: &groupID},
		User:    &User{ID: 2},
		Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		ChannelUsageFields: ChannelUsageFields{ChannelID: 9, OriginalModel: sent, ChannelMappedModel: sent,
			BillingModelSource: BillingModelSourceUpstream},
	}))
	require.NotNil(t, logs.lastLog)
	require.Equal(t, priority, optionalStringValue(logs.lastLog.ServiceTier))
	require.InDelta(t, expensive*100, logs.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expensive*100*group.RateMultiplier, logs.lastLog.ActualCost, 1e-12)
	require.InDelta(t, logs.lastLog.ActualCost, users.lastAmount, 1e-12)
}
