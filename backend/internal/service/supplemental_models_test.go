package service

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestSupplementalModelBilling(t *testing.T) {
	data, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := newStubPricingServiceFromJSON(t, string(data))
	// A future remote refresh must not reinstate cache-write charges or stale rates.
	remote := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-6-sol":  {InputCostPerToken: 99e-6, CacheCreationInputTokenCost: 2.5e-6},
		"gpt-6-luna": {InputCostPerToken: 99e-6, CacheCreationInputTokenCost: 0.125e-6},
	}}
	for _, tc := range []struct {
		model                 string
		input, cached, output float64
	}{
		{"gpt-6-sol", 2e-6, 0.2e-6, 10e-6},
		{"gpt-6-luna", 0.1e-6, 0.01e-6, 0.5e-6},
	} {
		for name, ps := range map[string]*PricingService{
			"billing_fallback": nil,
			"empty_remote":     {pricingData: map[string]*LiteLLMModelPricing{}},
			"local_catalog":    catalog,
			"remote_refresh":   remote,
		} {
			t.Run(tc.model+"/"+name, func(t *testing.T) {
				bs := NewBillingService(&config.Config{}, ps)
				for _, tier := range []struct {
					name       string
					multiplier float64
				}{
					{"", 1}, {"fast", 2}, {"priority", 2}, {"flex", 0.5},
				} {
					for _, total := range []int{272000, 272001} {
						tokens := UsageTokens{InputTokens: 100000, CacheCreationTokens: 100000, CacheReadTokens: total - 200000, OutputTokens: 100}
						cost, err := bs.CalculateCostWithServiceTier(tc.model, tokens, 1, tier.name)
						require.NoError(t, err)
						inputScale, outputScale := tier.multiplier, tier.multiplier
						if total > 272000 {
							inputScale *= 2
							outputScale *= 1.5
						}
						require.Equal(t, total > 272000, cost.LongContextBillingApplied)
						require.InDelta(t, 100000*tc.input*inputScale, cost.InputCost, 1e-12)
						require.InDelta(t, float64(total-200000)*tc.cached*inputScale, cost.CacheReadCost, 1e-12)
						require.InDelta(t, 100*tc.output*outputScale, cost.OutputCost, 1e-12)
						require.Zero(t, cost.CacheCreationCost)
					}
				}
				for _, model := range []string{tc.model, "openai/" + tc.model, tc.model + "-2026-09-23", tc.model + "-max"} {
					price, err := bs.GetModelPricing(model)
					require.NoError(t, err)
					require.InDelta(t, tc.input, price.InputPricePerToken, 1e-12)
					require.Zero(t, price.CacheCreationPricePerToken)
				}
				// Explicit channel pricing retains precedence over local defaults.
				customInput, customWrite := 7e-6, 3e-6
				price, err := bs.GetModelPricingWithChannel(tc.model, &ChannelModelPricing{InputPrice: &customInput, CacheWritePrice: &customWrite})
				require.NoError(t, err)
				require.Equal(t, customInput, price.InputPricePerToken)
				require.Equal(t, customWrite, price.CacheCreationPricePerToken)
			})
		}
	}
}

func TestSupplementalModelCapabilities(t *testing.T) {
	ps := &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		require.Contains(t, openai.DefaultModelIDs(), model)
		require.Contains(t, ps.ListModelNamesByProvider("openai"), model)
		require.NotNil(t, ps.GetIdentifiedModelPricing(model))
		for _, alias := range []string{model, "openai/" + model, model + "-2026-09-23", model + "-max", model + "-openai-compact"} {
			require.Equal(t, model, normalizeCodexModel(alias))
			require.Equal(t, model, getNormalizedCodexModel(alias))
			require.True(t, supportsOpenAIReasoningEffortMax(alias))
			require.False(t, isOpenAIGPT6AstraModel(alias))
		}
		require.Empty(t, getNormalizedCodexModel(model+"-unknown"))
		require.True(t, isOpenAICodexImageInputModel(model))
		descriptor := newConfiguredCodexModelDescriptor(model)
		require.Equal(t, int64(1050000), descriptor.ContextWindow)
		require.Equal(t, int64(1050000), descriptor.MaxContextWindow)
		require.Equal(t, "medium", *descriptor.DefaultReasoningLevel)
		var efforts []string
		for _, level := range descriptor.SupportedReasoningLevels {
			efforts = append(efforts, level.Effort)
		}
		require.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, efforts)
		require.True(t, configuredCodexSupportsPriorityServiceTier(model))
		require.False(t, configuredCodexSupportsUltrafastServiceTier(model))
		body, err := adjustAPIKeyCodexModelsManifest([]byte(`{"models":[{"slug":"`+model+`","use_responses_lite":true}]}`), nil)
		require.NoError(t, err)
		var result struct {
			Models []struct {
				UseResponsesLite bool `json:"use_responses_lite"`
			} `json:"models"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		require.False(t, result.Models[0].UseResponsesLite)
	}
	require.Equal(t, "gpt-6-astra", normalizeKnownOpenAICodexModel("gpt-6"))
}
