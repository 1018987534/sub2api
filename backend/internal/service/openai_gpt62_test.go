//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestGPT62IdentityEffortAndTools(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		require.Contains(t, openai.DefaultModelIDs(), model)
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			alias := "openai/" + model + "-" + effort
			require.Equal(t, model, openai.GPT62ModelID(alias))
			require.Equal(t, model, normalizeCodexModel(alias))
			require.Equal(t, model, normalizeKnownOpenAICodexModel(alias))
			require.Equal(t, model, NormalizeOpenAICompatRequestedModel(alias))
			require.Equal(t, effort, normalizeOpenAIReasoningEffortForModel(effort, model))
			req := &apicompat.AnthropicRequest{Model: alias}
			applyOpenAICompatModelNormalization(req)
			require.Equal(t, model, req.Model)
			require.Equal(t, effort, req.OutputConfig.Effort)
			converted, err := apicompat.AnthropicToResponses(req)
			require.NoError(t, err)
			require.Equal(t, effort, converted.Reasoning.Effort)
		}
		for _, effort := range []string{"none", "minimal"} {
			require.ErrorContains(t, validateGPT61CompatRequest([]byte("{\"reasoning\":{\"effort\":\""+effort+"\"}}"), model), model)
			require.Error(t, validateGPT61CompatRequest([]byte("{\"model\":\""+model+"-"+effort+"\"}"), model))
			_, err := apicompat.ChatCompletionsToResponses(&apicompat.ChatCompletionsRequest{Model: model, ReasoningEffort: effort})
			require.ErrorContains(t, err, model)
		}
		require.Error(t, validateGPT61CompatRequest([]byte("{\"thinking\":{\"type\":\"disabled\"}}"), model))
		require.True(t, openai.RequiresResponsesForToolCalls(model))
		require.True(t, shouldAutoInjectPromptCacheKeyForCompat(model))
		require.NotEmpty(t, openai.CodexBaseInstructionsForModel(model))
		body, changed, err := normalizeGPT6ResponsesSampling([]byte("{\"temperature\":1,\"top_p\":0.5}"), model)
		require.NoError(t, err)
		require.True(t, changed)
		require.JSONEq(t, "{}", string(body))
	}
	require.Equal(t, "gpt-5.6-sol", openai.DefaultModelIDs()[0])
	for _, invalid := range []string{"gpt-6.2-sol-preview", "gpt-6.2-astra-pro", "gpt-6.2-terra", "gpt-6.1-sol"} {
		require.False(t, openai.IsGPT62ModelSpelling(invalid))
	}
}

func TestGPT62OfficialManifestAndAccountCapabilities(t *testing.T) {
	for _, tc := range []struct {
		model    string
		window   int
		metadata []byte
	}{
		{"gpt-6.2-sol", 500000, openai.CodexGPT62SolMetadata},
		{"gpt-6.2-astra", 272000, openai.CodexGPT62AstraMetadata},
	} {
		t.Run(tc.model, func(t *testing.T) {
			body, err := BuildCodexModelsManifest([]string{tc.model})
			require.NoError(t, err)
			models := decodeCodexManifestModels(t, body)
			require.Len(t, models, 1)
			m := models[0]
			require.Equal(t, tc.model, m["slug"])
			require.EqualValues(t, tc.window, m["context_window"])
			require.EqualValues(t, 872000, m["max_context_window"])
			require.Equal(t, []string{"low", "medium", "high", "xhigh", "max", "ultra"}, effortsFromManifestModel(t, m))
			require.Equal(t, "0.166.0", m["minimal_client_version"])
			require.Equal(t, "xhigh", m["multi_agent_reasoning_effort"])
			require.Equal(t, "v2", m["multi_agent_version"])
			require.Equal(t, tc.metadata, []byte(newConfiguredCodexModelDescriptor(tc.model).officialMetadata))
			var official map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(tc.metadata, &official))
			var messages struct {
				InstructionsTemplate string `json:"instructions_template"`
			}
			require.NoError(t, json.Unmarshal(official["model_messages"], &messages))
			require.Equal(t, messages.InstructionsTemplate, openai.CodexBaseInstructionsForModel(tc.model))
			apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}
			caps := accountCodexToolCapabilities(apiKey, tc.model)
			require.Equal(t, "false", string(caps["use_responses_lite"]))
			require.True(t, bytes.Contains(caps["service_tiers"], []byte("ultrafast")))
			adjusted, err := adjustAPIKeyCodexModelsManifest([]byte("{\"models\":[{\"slug\":\""+tc.model+"\",\"use_responses_lite\":true}]}"), apiKey)
			require.NoError(t, err)
			require.Contains(t, string(adjusted), "\"use_responses_lite\":false")
			oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"base_url": "https://chatgpt.com"}}
			caps = accountCodexToolCapabilities(oauth, tc.model)
			require.Equal(t, "true", string(caps["use_responses_lite"]))
			require.False(t, bytes.Contains(caps["service_tiers"], []byte("ultrafast")))
		})
	}
}

func TestGPT62PricingAcrossSourcesTiersAndCacheTTL(t *testing.T) {
	raw, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := newStubPricingServiceFromJSON(t, string(raw))
	intro := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for _, source := range []struct {
		name    string
		pricing *PricingService
	}{
		{"catalog", catalog}, {"remote-missing", &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}}, {"billing-fallback", nil},
	} {
		for _, tc := range []struct {
			model                 string
			input, output, cached float64
		}{
			{"gpt-6.2-sol", 2e-6, 10e-6, .1e-6}, {"gpt-6.2-astra", 8e-6, 40e-6, .4e-6},
		} {
			t.Run(source.name+"/"+tc.model, func(t *testing.T) {
				s := NewBillingService(&config.Config{}, source.pricing)
				p, err := s.getModelPricingAt(tc.model, intro)
				require.NoError(t, err)
				require.InDelta(t, tc.input, p.InputPricePerToken, 1e-12)
				require.InDelta(t, tc.output, p.OutputPricePerToken, 1e-12)
				require.InDelta(t, tc.cached, p.CacheReadPricePerToken, 1e-12)
				require.InDelta(t, tc.input*1.25, p.CacheCreationPricePerToken, 1e-12)
				require.NotNil(t, p.CacheCreation12hPricePerToken)
				require.InDelta(t, tc.input*2.5, *p.CacheCreation12hPricePerToken, 1e-12)
				require.Equal(t, 3., p.UltrafastMultiplier)
				for _, tier := range []struct {
					name  string
					scale float64
				}{{"", 1}, {"priority", 2}, {"ultrafast", 3}} {
					for _, input := range []int{272000, 272001} {
						tokens := UsageTokens{InputTokens: input - 3000, CacheReadTokens: 1000, CacheCreationTokens: 2000, CacheCreation30mTokens: 1000, CacheCreation12hTokens: 1000, OutputTokens: 100}
						c := s.computeTokenBreakdown(p, tokens, 1, tier.name, true)
						inScale, outScale := 1., 1.
						if input > 272000 {
							inScale = 2
							outScale = 1.5
						}
						require.Equal(t, input > 272000, c.LongContextBillingApplied)
						require.InDelta(t, float64(tokens.InputTokens)*tc.input*tier.scale*inScale, c.InputCost, 1e-10)
						require.InDelta(t, 1000*tc.cached*tier.scale*inScale, c.CacheReadCost, 1e-10)
						require.InDelta(t, 1000*tc.input*(1.25+2.5)*tier.scale*inScale, c.CacheCreationCost, 1e-10)
						require.InDelta(t, 100*tc.output*tier.scale*outScale, c.OutputCost, 1e-10)
					}
				}
			})
		}
	}
}

func TestGPT62UltrafastPacificPromotionBoundary(t *testing.T) {
	boundary := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		require.Equal(t, 3., openAIModelUltrafastPricingRatioAt(model, boundary.Add(-time.Nanosecond)))
		require.Equal(t, 6., openAIModelUltrafastPricingRatioAt(model, boundary))
	}
	require.Equal(t, 6., openAIModelUltrafastPricingRatioAt("gpt-6-astra", boundary.Add(-time.Hour)))
	require.Equal(t, 6., openAIModelUltrafastPricingRatioAt("gpt-6.1-sol", boundary.Add(-time.Hour)))
	require.Zero(t, openAIModelUltrafastPricingRatioAt("gpt-6.1-terra", boundary))
}
