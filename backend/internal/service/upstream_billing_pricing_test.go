//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func upstreamPricingTestAccount(prices map[string]UpstreamBillingModelPrice) *Account {
	return &Account{
		ID:       12017,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			UpstreamBillingPriceSyncEnabledExtraKey:  true,
			UpstreamBillingManualModelPricesExtraKey: prices,
		},
	}
}

func lunaUpstreamPrice() UpstreamBillingModelPrice {
	return UpstreamBillingModelPrice{
		InputPricePerToken:              1e-6,
		OutputPricePerToken:             6e-6,
		CacheCreationPricePerToken:      1.25e-6,
		CacheReadPricePerToken:          0.1e-6,
		LongContextInputTokenThreshold:  openAIGPT54LongContextInputThreshold,
		LongContextInputCostMultiplier:  openAIGPT54LongContextInputMultiplier,
		LongContextOutputCostMultiplier: openAIGPT54LongContextOutputMultiplier,
	}
}

func TestSyncedUpstreamBillingPriceMatchesUpdatedLocalLunaPrice(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	local, err := billing.CalculateCost("gpt-5.6-luna", UsageTokens{InputTokens: 100_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.1, local.ActualCost, 1e-12)

	account := upstreamPricingTestAccount(map[string]UpstreamBillingModelPrice{
		"gpt-5.6-luna": lunaUpstreamPrice(),
	})
	selection, ok := resolveSyncedUpstreamBillingModel(account, "gpt-5.4-nano", "gpt-5.6-luna", false, true)
	require.True(t, ok)
	require.Equal(t, "gpt-5.6-luna", selection.Model)

	actual, ok := calculateSyncedUpstreamTokenCost(
		billing, selection, UsageTokens{InputTokens: 100_000}, 1, "", true,
	)
	require.True(t, ok)
	require.InDelta(t, 0.1, actual.ActualCost, 1e-12)
	require.InDelta(t, 1.0, actual.InputPricePerToken*1_000_000, 1e-12)
	require.InDelta(t, local.ActualCost, actual.ActualCost, 1e-12)
}

func TestOpenAIGatewayRecordUsageBillsPricierResponseModelFromSnapshot(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	account := upstreamPricingTestAccount(map[string]UpstreamBillingModelPrice{
		"gpt-5.4-nano": {
			InputPricePerToken:  0.2e-6,
			OutputPricePerToken: 1.25e-6,
		},
		"gpt-5.6-luna": lunaUpstreamPrice(),
	})

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:             "upstream_snapshot_luna_price_increase",
			Model:                 "gpt-5.4-nano",
			UpstreamModel:         "gpt-5.4-nano",
			UpstreamResponseModel: "gpt-5.6-luna",
			Usage:                 OpenAIUsage{InputTokens: 1_000_000},
			Duration:              time.Second,
		},
		APIKey:  &APIKey{ID: 10},
		User:    &User{ID: 20},
		Account: account,
		ChannelUsageFields: ChannelUsageFields{
			ChannelID:          9,
			OriginalModel:      "gpt-5.4-nano",
			ChannelMappedModel: "gpt-5.4-nano",
			BillingModelSource: BillingModelSourceResponse,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 1.1, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 1.1, userRepo.lastAmount, 1e-12)
}

func TestSyncedUpstreamBillingModelFallbacks(t *testing.T) {
	account := upstreamPricingTestAccount(map[string]UpstreamBillingModelPrice{
		"sent-model": {
			InputPricePerToken:  0.7e-6,
			OutputPricePerToken: 3e-6,
		},
		"response-model": {
			InputPricePerToken:  1e-6,
			OutputPricePerToken: 6e-6,
		},
	})

	selection, ok := resolveSyncedUpstreamBillingModel(account, "sent-model", "response-model", false, true)
	require.True(t, ok)
	require.Equal(t, "response-model", selection.Model)

	selection, ok = resolveSyncedUpstreamBillingModel(account, "sent-model", "response-model", true, true)
	require.True(t, ok)
	require.Equal(t, "sent-model", selection.Model)

	selection, ok = resolveSyncedUpstreamBillingModel(account, "sent-model", "response-model", false, false)
	require.True(t, ok)
	require.Equal(t, "sent-model", selection.Model)

	selection, ok = resolveSyncedUpstreamBillingModel(account, "missing-sent", "missing-response", false, true)
	require.False(t, ok)
	require.Nil(t, selection)

	account.Extra[UpstreamBillingPriceSyncEnabledExtraKey] = false
	selection, ok = resolveSyncedUpstreamBillingModel(account, "sent-model", "response-model", false, true)
	require.False(t, ok)
	require.Nil(t, selection)
}

func TestHighestResolvedTokenPricingCollapsesIntervals(t *testing.T) {
	inputLow, inputHigh := 1e-6, 3e-6
	outputLow, outputHigh := 4e-6, 8e-6
	cacheLow, cacheHigh := 0.1e-6, 0.5e-6
	got := highestResolvedTokenPricing(&ResolvedPricing{
		Mode:        BillingModeToken,
		BasePricing: &ModelPricing{InputPricePerToken: inputLow, OutputPricePerToken: outputLow, CacheReadPricePerToken: cacheLow},
		Intervals: []PricingInterval{
			{InputPrice: &inputHigh, OutputPrice: &outputLow, CacheReadPrice: &cacheHigh},
			{InputPrice: &inputLow, OutputPrice: &outputHigh, CacheReadPrice: &cacheLow},
		},
	})

	require.NotNil(t, got)
	require.Equal(t, inputHigh, got.InputPricePerToken)
	require.Equal(t, outputHigh, got.OutputPricePerToken)
	require.Equal(t, cacheHigh, got.CacheReadPricePerToken)
	require.Zero(t, got.LongContextInputThreshold)
	require.Zero(t, got.LongContextInputMultiplier)
	require.Zero(t, got.LongContextOutputMultiplier)
}

func TestKeyBillingModelPricesIncludesCompleteLocalCatalogAndGroupOverride(t *testing.T) {
	groupID := int64(7)
	input, output, cacheWrite, cacheRead := 1e-6, 6e-6, 1.25e-6, 0.1e-6
	group := &Group{
		ID:       groupID,
		Platform: PlatformOpenAI,
		ModelPricing: []ChannelModelPricing{{
			Platform:   PlatformOpenAI,
			Models:     []string{"gpt-5.6-luna"},
			InputPrice: &input, OutputPrice: &output,
			CacheWritePrice: &cacheWrite, CacheReadPrice: &cacheRead,
		}},
	}
	billing := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, billing)

	prices := keyBillingModelPrices(context.Background(), &APIKey{GroupID: &groupID, Group: group}, resolver)
	require.Greater(t, len(prices), 10)
	require.Contains(t, prices, "gpt-5.4-nano")
	require.Contains(t, prices, "claude-sonnet-4")
	require.InDelta(t, 1.0, prices["gpt-5.6-luna"].InputPricePerToken*1_000_000, 1e-12)
	require.NotEmpty(t, UpstreamBillingModelPricesVersion(prices))
}

func TestParseUpstreamBillingProbeResponseAcceptsAndRejectsCatalogs(t *testing.T) {
	valid := `{
		"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token",
		"group_rate_multiplier":0.8,"resolved_rate_multiplier":0.8,
		"peak_rate_enabled":false,"effective_rate_multiplier":0.8,
		"observed_at":"2026-08-15T01:00:00Z",
		"model_prices":{"gpt-5.6-luna":{"input_price_per_token":0.000001,"output_price_per_token":0.000006}},
		"pricing_version":"sha256:catalog","pricing_observed_at":"2026-08-15T01:00:00Z",
		"inferred_model_prices":{"gpt-5.6-luna":{"input_price_per_token":0.000001,"output_price_per_token":0.000006,"sample_count":3,"input_sample_count":3,"output_sample_count":2,"observed_at":"2026-08-15T01:00:00Z"}}
	}`
	data, err := parseUpstreamBillingProbeResponse([]byte(valid))
	require.NoError(t, err)
	prices, ok := parseUpstreamBillingModelPrices(data[upstreamBillingModelPricesDataKey])
	require.True(t, ok)
	require.InDelta(t, 1.0, prices["gpt-5.6-luna"].InputPricePerToken*1_000_000, 1e-12)
	inferred, ok := parseUpstreamBillingInferredModelPrices(data[upstreamBillingInferredModelPricesDataKey])
	require.True(t, ok)
	require.Equal(t, 3, inferred["gpt-5.6-luna"].SampleCount)

	invalid := `{
		"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token",
		"group_rate_multiplier":0.8,"resolved_rate_multiplier":0.8,
		"peak_rate_enabled":false,"effective_rate_multiplier":0.8,
		"observed_at":"2026-08-15T01:00:00Z",
		"model_prices":{"gpt-5.6-luna":{"input_price_per_token":-1,"output_price_per_token":0.000006}},
		"pricing_version":"sha256:catalog","pricing_observed_at":"2026-08-15T01:00:00Z"
	}`
	_, err = parseUpstreamBillingProbeResponse([]byte(invalid))
	require.ErrorContains(t, err, "invalid billing model prices")
}

func TestInferUpstreamBillingPricesUsesHighestObservedComponentRates(t *testing.T) {
	billingMode := string(BillingModeToken)
	standardTier := "standard"
	now := time.Date(2026, time.August, 15, 2, 0, 0, 0, time.UTC)
	got := inferUpstreamBillingPricesFromUsageLogs([]UsageLog{
		{
			Model: "gpt-5.6-luna", BillingMode: &billingMode, ServiceTier: &standardTier,
			InputTokens: 1_000_000, InputCost: 0.2,
			OutputTokens: 100_000, OutputCost: 0.6,
			CacheReadTokens: 1_000_000, CacheReadCost: 0.1,
			CreatedAt: now.Add(-time.Hour),
		},
		{
			Model: "GPT-5.6-LUNA", BillingMode: &billingMode,
			InputTokens: 1_000_000, InputCost: 1,
			OutputTokens: 100_000, OutputCost: 0.6,
			CreatedAt: now,
		},
	})

	require.Contains(t, got, "gpt-5.6-luna")
	price := got["gpt-5.6-luna"]
	require.InDelta(t, 1, price.InputPricePerToken*1_000_000, 1e-12)
	require.InDelta(t, 6, price.OutputPricePerToken*1_000_000, 1e-12)
	require.InDelta(t, 0.1, price.CacheReadPricePerToken*1_000_000, 1e-12)
	require.Equal(t, 2, price.SampleCount)
	require.Equal(t, 2, price.InputSampleCount)
	require.Equal(t, now, price.ObservedAt)
}

func TestInferUpstreamBillingPricesSkipsTierAndLongContextRows(t *testing.T) {
	priority := "priority"
	got := inferUpstreamBillingPricesFromUsageLogs([]UsageLog{
		{Model: "gpt-5.6-luna", ServiceTier: &priority, InputTokens: 1_000_000, InputCost: 2},
		{Model: "gpt-5.6-luna", LongContextBillingApplied: true, InputTokens: 1_000_000, InputCost: 2},
	})
	require.Empty(t, got)
}

func TestUpstreamBillingPriceDiscrepancyRequiresManualConfirmation(t *testing.T) {
	now := time.Date(2026, time.August, 15, 2, 0, 0, 0, time.UTC)
	account := &Account{
		ID: 12017, Name: "plus-shayu-plus", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Status: StatusActive,
		Extra: map[string]any{
			UpstreamBillingPriceSyncEnabledExtraKey: true,
			UpstreamBillingManualModelPricesExtraKey: map[string]UpstreamBillingModelPrice{
				"gpt-5.6-luna": {InputPricePerToken: 0.2e-6, OutputPricePerToken: 6e-6},
			},
			UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{
				Status: UpstreamBillingProbeStatusOK,
				Data: map[string]any{
					upstreamBillingInferredModelPricesDataKey: map[string]UpstreamBillingInferredModelPrice{
						"gpt-5.6-luna": {
							UpstreamBillingModelPrice: UpstreamBillingModelPrice{
								InputPricePerToken: 1e-6, OutputPricePerToken: 6e-6,
							},
							SampleCount: 3, InputSampleCount: 3, OutputSampleCount: 2, ObservedAt: now,
						},
					},
				},
			},
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := NewUpstreamBillingProbeService(repo, nil, nil)

	items, err := svc.ListPriceDiscrepancies(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.InDelta(t, 0.2, items[0].CurrentPrice.InputPricePerToken*1_000_000, 1e-12)
	require.InDelta(t, 1, items[0].InferredPrice.InputPricePerToken*1_000_000, 1e-12)

	_, err = svc.ConfirmInferredPrice(context.Background(), account.ID, "gpt-5.6-luna")
	require.NoError(t, err)
	manual, ok := parseUpstreamBillingModelPrices(account.Extra[UpstreamBillingManualModelPricesExtraKey])
	require.True(t, ok)
	require.InDelta(t, 1, manual["gpt-5.6-luna"].InputPricePerToken*1_000_000, 1e-12)

	items, err = svc.ListPriceDiscrepancies(context.Background())
	require.NoError(t, err)
	require.Empty(t, items)
}
