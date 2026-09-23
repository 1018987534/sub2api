package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/openai"

func isSupplementalOpenAIModel(model string) bool {
	return openai.NormalizeSupplementalModel(model) != ""
}

// Supplemental models use explicit local rates, including the user's free
// cache-write policy, even when a remote catalog lags or restores write charges.
// Group/channel prices still override this default through the existing resolver.
func supplementalModelPricing(model string) *LiteLLMModelPricing {
	if price := supplementalPricing[openai.NormalizeSupplementalModel(model)]; price != nil {
		copy := *price
		return &copy
	}
	return nil
}
