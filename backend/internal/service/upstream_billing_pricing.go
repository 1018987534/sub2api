package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
)

const (
	// UpstreamBillingPriceSyncEnabledExtraKey opts an API-key account into using
	// the periodically probed upstream price catalog on the request billing path.
	UpstreamBillingPriceSyncEnabledExtraKey = "upstream_billing_price_sync_enabled"
	// UpstreamBillingManualModelPricesExtraKey stores operator-confirmed prices
	// for an older upstream or an operator-confirmed correction. Confirmed prices
	// take precedence for the named models until another candidate is confirmed.
	UpstreamBillingManualModelPricesExtraKey = "upstream_billing_manual_model_prices"

	upstreamBillingModelPricesDataKey         = "model_prices"
	upstreamBillingInferredModelPricesDataKey = "inferred_model_prices"
	upstreamBillingPricingVersionDataKey      = "pricing_version"
	upstreamBillingPricingObservedDataKey     = "pricing_observed_at"
	upstreamBillingPricingSource              = "upstream_sync"
	upstreamBillingManualPricingSource        = "upstream_manual"
)

// UpstreamBillingModelPrice is the token price contract published by
// /v1/sub2api/billing. All monetary fields are USD per token.
type UpstreamBillingModelPrice struct {
	InputPricePerToken                 float64 `json:"input_price_per_token"`
	InputPricePerTokenPriority         float64 `json:"input_price_per_token_priority,omitempty"`
	OutputPricePerToken                float64 `json:"output_price_per_token"`
	OutputPricePerTokenPriority        float64 `json:"output_price_per_token_priority,omitempty"`
	CacheCreationPricePerToken         float64 `json:"cache_creation_price_per_token,omitempty"`
	CacheCreationPricePerTokenPriority float64 `json:"cache_creation_price_per_token_priority,omitempty"`
	CacheCreation5mPrice               float64 `json:"cache_creation_5m_price,omitempty"`
	CacheCreation1hPrice               float64 `json:"cache_creation_1h_price,omitempty"`
	CacheReadPricePerToken             float64 `json:"cache_read_price_per_token,omitempty"`
	CacheReadPricePerTokenPriority     float64 `json:"cache_read_price_per_token_priority,omitempty"`
	LongContextInputTokenThreshold     int     `json:"long_context_input_token_threshold,omitempty"`
	LongContextInputCostMultiplier     float64 `json:"long_context_input_cost_multiplier,omitempty"`
	LongContextOutputCostMultiplier    float64 `json:"long_context_output_cost_multiplier,omitempty"`
	SupportsCacheBreakdown             bool    `json:"supports_cache_breakdown,omitempty"`
}

// UpstreamBillingPricingSelection is retained on CostBreakdown and structured
// logs so a production usage row can be reconciled to the catalog snapshot.
type UpstreamBillingPricingSelection struct {
	Model   string
	Source  string
	Version string
	Pricing *ModelPricing
}

func validUpstreamBillingModelPrice(price UpstreamBillingModelPrice) bool {
	values := []float64{
		price.InputPricePerToken,
		price.InputPricePerTokenPriority,
		price.OutputPricePerToken,
		price.OutputPricePerTokenPriority,
		price.CacheCreationPricePerToken,
		price.CacheCreationPricePerTokenPriority,
		price.CacheCreation5mPrice,
		price.CacheCreation1hPrice,
		price.CacheReadPricePerToken,
		price.CacheReadPricePerTokenPriority,
		price.LongContextInputCostMultiplier,
		price.LongContextOutputCostMultiplier,
	}
	for _, value := range values {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	if price.InputPricePerToken <= 0 && price.OutputPricePerToken <= 0 {
		return false
	}
	if price.LongContextInputTokenThreshold < 0 {
		return false
	}
	if price.LongContextInputTokenThreshold > 0 &&
		(price.LongContextInputCostMultiplier <= 0 || price.LongContextOutputCostMultiplier <= 0) {
		return false
	}
	return true
}

func (price UpstreamBillingModelPrice) toModelPricing() *ModelPricing {
	if !validUpstreamBillingModelPrice(price) {
		return nil
	}
	cacheCreation5m := price.CacheCreation5mPrice
	if cacheCreation5m == 0 {
		cacheCreation5m = price.CacheCreationPricePerToken
	}
	cacheCreation1h := price.CacheCreation1hPrice
	if cacheCreation1h == 0 {
		cacheCreation1h = price.CacheCreationPricePerToken
	}
	return &ModelPricing{
		InputPricePerToken:                 price.InputPricePerToken,
		InputPricePerTokenPriority:         price.InputPricePerTokenPriority,
		OutputPricePerToken:                price.OutputPricePerToken,
		OutputPricePerTokenPriority:        price.OutputPricePerTokenPriority,
		CacheCreationPricePerToken:         price.CacheCreationPricePerToken,
		CacheCreationPricePerTokenPriority: price.CacheCreationPricePerTokenPriority,
		CacheCreationPriceExplicit:         true,
		CacheCreation5mPrice:               cacheCreation5m,
		CacheCreation1hPrice:               cacheCreation1h,
		CacheReadPricePerToken:             price.CacheReadPricePerToken,
		CacheReadPricePerTokenPriority:     price.CacheReadPricePerTokenPriority,
		LongContextInputThreshold:          price.LongContextInputTokenThreshold,
		LongContextInputMultiplier:         price.LongContextInputCostMultiplier,
		LongContextOutputMultiplier:        price.LongContextOutputCostMultiplier,
		SupportsCacheBreakdown:             price.SupportsCacheBreakdown,
	}
}

func upstreamBillingModelPriceFromModelPricing(pricing *ModelPricing) (UpstreamBillingModelPrice, bool) {
	if pricing == nil {
		return UpstreamBillingModelPrice{}, false
	}
	price := UpstreamBillingModelPrice{
		InputPricePerToken:                 pricing.InputPricePerToken,
		InputPricePerTokenPriority:         pricing.InputPricePerTokenPriority,
		OutputPricePerToken:                pricing.OutputPricePerToken,
		OutputPricePerTokenPriority:        pricing.OutputPricePerTokenPriority,
		CacheCreationPricePerToken:         pricing.CacheCreationPricePerToken,
		CacheCreationPricePerTokenPriority: pricing.CacheCreationPricePerTokenPriority,
		CacheCreation5mPrice:               pricing.CacheCreation5mPrice,
		CacheCreation1hPrice:               pricing.CacheCreation1hPrice,
		CacheReadPricePerToken:             pricing.CacheReadPricePerToken,
		CacheReadPricePerTokenPriority:     pricing.CacheReadPricePerTokenPriority,
		LongContextInputTokenThreshold:     pricing.LongContextInputThreshold,
		LongContextInputCostMultiplier:     pricing.LongContextInputMultiplier,
		LongContextOutputCostMultiplier:    pricing.LongContextOutputMultiplier,
		SupportsCacheBreakdown:             pricing.SupportsCacheBreakdown,
	}
	return price, validUpstreamBillingModelPrice(price)
}

func normalizeUpstreamBillingModelPrices(raw map[string]UpstreamBillingModelPrice) (map[string]UpstreamBillingModelPrice, bool) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, false
	}
	result := make(map[string]UpstreamBillingModelPrice, len(raw))
	for model, price := range raw {
		model = strings.TrimSpace(model)
		if model == "" || len([]rune(model)) > upstreamResponseModelMaxLength || !validUpstreamBillingModelPrice(price) {
			return nil, false
		}
		result[strings.ToLower(model)] = price
	}
	return result, len(result) > 0
}

// UpstreamBillingModelPricesVersion returns a stable content identifier for a
// normalized catalog. encoding/json orders string map keys deterministically.
func UpstreamBillingModelPricesVersion(prices map[string]UpstreamBillingModelPrice) string {
	normalized, ok := normalizeUpstreamBillingModelPrices(prices)
	if !ok {
		return ""
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body))
}

func parseUpstreamBillingModelPrices(value any) (map[string]UpstreamBillingModelPrice, bool) {
	if value == nil {
		return nil, false
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var prices map[string]UpstreamBillingModelPrice
	if err := json.Unmarshal(body, &prices); err != nil {
		return nil, false
	}
	return normalizeUpstreamBillingModelPrices(prices)
}

func upstreamBillingPriceSyncEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, _ := account.Extra[UpstreamBillingPriceSyncEnabledExtraKey].(bool)
	return enabled
}

func lookupUpstreamBillingModelPrice(prices map[string]UpstreamBillingModelPrice, model string) (UpstreamBillingModelPrice, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return UpstreamBillingModelPrice{}, false
	}
	candidates := []string{model}
	if slash := strings.LastIndex(model, "/"); slash >= 0 && slash+1 < len(model) {
		candidates = append(candidates, model[slash+1:])
	}
	for _, candidate := range candidates {
		if price, ok := prices[candidate]; ok {
			return price, true
		}
	}
	return UpstreamBillingModelPrice{}, false
}

func resolveUpstreamBillingPricing(account *Account, model string) (*UpstreamBillingPricingSelection, bool) {
	if !upstreamBillingPriceSyncEnabled(account) {
		return nil, false
	}
	if prices, valid := parseUpstreamBillingModelPrices(account.Extra[UpstreamBillingManualModelPricesExtraKey]); valid {
		if price, found := lookupUpstreamBillingModelPrice(prices, model); found {
			return &UpstreamBillingPricingSelection{
				Model: strings.TrimSpace(model), Source: upstreamBillingManualPricingSource,
				Version: "manual", Pricing: price.toModelPricing(),
			}, true
		}
	}
	var version string
	if snapshot, ok := account.Extra[UpstreamBillingProbeExtraKey].(map[string]any); ok {
		if data, ok := snapshot["data"].(map[string]any); ok {
			version, _ = data[upstreamBillingPricingVersionDataKey].(string)
			if prices, valid := parseUpstreamBillingModelPrices(data[upstreamBillingModelPricesDataKey]); valid {
				if price, found := lookupUpstreamBillingModelPrice(prices, model); found {
					return &UpstreamBillingPricingSelection{
						Model: strings.TrimSpace(model), Source: upstreamBillingPricingSource,
						Version: strings.TrimSpace(version), Pricing: price.toModelPricing(),
					}, true
				}
			}
		}
	}
	return nil, false
}

func resolveSyncedUpstreamBillingModel(account *Account, sentModel, responseModel string, responseConflict, responseModelEnabled bool) (*UpstreamBillingPricingSelection, bool) {
	if responseModelEnabled && !responseConflict {
		if selection, ok := resolveUpstreamBillingPricing(account, responseModel); ok {
			return selection, true
		}
	}
	return resolveUpstreamBillingPricing(account, sentModel)
}

func calculateSyncedUpstreamTokenCost(
	billingService *BillingService,
	selection *UpstreamBillingPricingSelection,
	tokens UsageTokens,
	rateMultiplier float64,
	serviceTier string,
	longContextBillingEnabled bool,
) (*CostBreakdown, bool) {
	if billingService == nil || selection == nil || selection.Pricing == nil {
		return nil, false
	}
	cost := billingService.computeTokenBreakdown(
		selection.Pricing, tokens, rateMultiplier, serviceTier, longContextBillingEnabled,
	)
	if cost == nil {
		return nil, false
	}
	cost.PricingSource = selection.Source
	cost.PricingModel = selection.Model
	cost.PricingVersion = selection.Version
	return cost, true
}

func logSyncedUpstreamPricingApplied(component string, account *Account, requestID string, cost *CostBreakdown) {
	if cost == nil {
		return
	}
	attrs := []any{
		"component", component,
		"request_id", strings.TrimSpace(requestID),
		"pricing_source", cost.PricingSource,
		"pricing_model", cost.PricingModel,
		"pricing_version", cost.PricingVersion,
		"input_price_per_million", cost.InputPricePerToken * 1_000_000,
		"output_price_per_million", cost.OutputPricePerToken * 1_000_000,
		"cache_write_price_per_million", cost.CacheCreationPricePerToken * 1_000_000,
		"cache_read_price_per_million", cost.CacheReadPricePerToken * 1_000_000,
		"total_cost", cost.TotalCost,
		"actual_cost", cost.ActualCost,
	}
	if account != nil {
		attrs = append(attrs, "account_id", account.ID, "platform", account.Platform)
	}
	slog.Info("billing.upstream_pricing_applied", attrs...)
}

func keyBillingModelPrices(ctx context.Context, apiKey *APIKey, resolver *ModelPricingResolver) map[string]UpstreamBillingModelPrice {
	if apiKey == nil || apiKey.Group == nil || apiKey.GroupID == nil || resolver == nil || resolver.billingService == nil {
		return nil
	}
	modelSet := make(map[string]struct{})
	for _, model := range resolver.billingService.ListKnownTokenPricingModels() {
		model = strings.ToLower(strings.TrimSpace(model))
		if model != "" {
			modelSet[model] = struct{}{}
		}
	}
	for i := range apiKey.Group.ModelPricing {
		for _, model := range apiKey.Group.ModelPricing[i].Models {
			model = strings.ToLower(strings.TrimSpace(model))
			if model != "" && !strings.Contains(model, "*") {
				modelSet[model] = struct{}{}
			}
		}
	}
	if resolver.channelService != nil {
		if channel, err := resolver.channelService.GetChannelForGroup(ctx, *apiKey.GroupID); err == nil && channel != nil {
			for i := range channel.ModelPricing {
				for _, model := range channel.ModelPricing[i].Models {
					model = strings.ToLower(strings.TrimSpace(model))
					if model != "" && !strings.Contains(model, "*") {
						modelSet[model] = struct{}{}
					}
				}
			}
		}
	}

	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)

	result := make(map[string]UpstreamBillingModelPrice, len(models))
	for _, model := range models {
		resolved := resolver.Resolve(ctx, PricingInput{Model: model, GroupID: apiKey.GroupID, Group: apiKey.Group})
		if price, ok := upstreamBillingModelPriceFromModelPricing(highestResolvedTokenPricing(resolved)); ok {
			result[model] = price
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func highestResolvedTokenPricing(resolved *ResolvedPricing) *ModelPricing {
	if resolved == nil || resolved.Mode != BillingModeToken || resolved.BasePricing == nil {
		return nil
	}
	highest := *resolved.BasePricing
	for i := range resolved.Intervals {
		candidate := intervalToModelPricing(&resolved.Intervals[i], resolved.SupportsCacheBreakdown, resolved.channelPricing)
		highest.InputPricePerToken = math.Max(highest.InputPricePerToken, candidate.InputPricePerToken)
		highest.InputPricePerTokenPriority = math.Max(highest.InputPricePerTokenPriority, candidate.InputPricePerTokenPriority)
		highest.OutputPricePerToken = math.Max(highest.OutputPricePerToken, candidate.OutputPricePerToken)
		highest.OutputPricePerTokenPriority = math.Max(highest.OutputPricePerTokenPriority, candidate.OutputPricePerTokenPriority)
		highest.CacheCreationPricePerToken = math.Max(highest.CacheCreationPricePerToken, candidate.CacheCreationPricePerToken)
		highest.CacheCreationPricePerTokenPriority = math.Max(highest.CacheCreationPricePerTokenPriority, candidate.CacheCreationPricePerTokenPriority)
		highest.CacheCreation5mPrice = math.Max(highest.CacheCreation5mPrice, candidate.CacheCreation5mPrice)
		highest.CacheCreation1hPrice = math.Max(highest.CacheCreation1hPrice, candidate.CacheCreation1hPrice)
		highest.CacheReadPricePerToken = math.Max(highest.CacheReadPricePerToken, candidate.CacheReadPricePerToken)
		highest.CacheReadPricePerTokenPriority = math.Max(highest.CacheReadPricePerTokenPriority, candidate.CacheReadPricePerTokenPriority)
		highest.CacheCreationPriceExplicit = highest.CacheCreationPriceExplicit || candidate.CacheCreationPriceExplicit
		highest.SupportsCacheBreakdown = highest.SupportsCacheBreakdown || candidate.SupportsCacheBreakdown
	}
	if len(resolved.Intervals) > 0 {
		// The interval ladder has already been flattened to its maximum values.
		// Retaining a separate long-context multiplier would apply a second uplift.
		highest.LongContextInputThreshold = 0
		highest.LongContextInputMultiplier = 0
		highest.LongContextOutputMultiplier = 0
	}
	return &highest
}

// KeyBillingModelPrices returns effective flat group price cards for a
// downstream Sub2API instance. The endpoint is probed out of band.
func (s *GatewayService) KeyBillingModelPrices(ctx context.Context, apiKey *APIKey) map[string]UpstreamBillingModelPrice {
	if s == nil {
		return nil
	}
	return keyBillingModelPrices(ctx, apiKey, s.resolver)
}

// KeyBillingModelPrices is the OpenAI gateway equivalent.
func (s *OpenAIGatewayService) KeyBillingModelPrices(ctx context.Context, apiKey *APIKey) map[string]UpstreamBillingModelPrice {
	if s == nil {
		return nil
	}
	return keyBillingModelPrices(ctx, apiKey, s.resolver)
}
