package service

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const ModelPricingUnavailableCode = "model_pricing_unavailable"
const ModelPricingUnavailableMessage = "Billing price is not configured for this model. Please contact the administrator."

func hasConfiguredUnitPrice(values ...*float64) bool {
	for _, price := range values {
		if price != nil && *price >= 0 && !math.IsInf(*price, 0) {
			return true
		}
	}
	return false
}

func intervalHasTokenPrice(interval *PricingInterval) bool {
	return hasConfiguredUnitPrice(interval.InputPrice, interval.OutputPrice, interval.CacheReadPrice,
		interval.CacheWritePrice, interval.CacheWrite1hPrice, interval.CacheWrite12hPrice)
}

// hasUsablePrice distinguishes explicit zero from missing prices. Interval-only
// cards must cover every possible context size. A base card can legitimately
// charge just one kind of token, as in input-only embedding models.
func (p *ResolvedPricing) hasUsablePrice(longContextEnabled bool) bool {
	if p == nil {
		return false
	}
	if p.Mode == BillingModeToken || p.Mode == "" {
		if p.BasePricing != nil {
			return true
		}
		if !longContextEnabled {
			interval := FindMatchingInterval(p.Intervals, 1)
			return interval != nil && intervalHasTokenPrice(interval)
		}
		return pricingIntervalsCoverAllTokens(p.Intervals, intervalHasTokenPrice)
	}
	if p.channelPricing != nil && hasConfiguredUnitPrice(p.channelPricing.PerRequestPrice) {
		return true
	}
	if p.Mode == BillingModePerRequest {
		return pricingIntervalsCoverAllTokens(p.RequestTiers, func(interval *PricingInterval) bool {
			return hasConfiguredUnitPrice(interval.PerRequestPrice)
		})
	}
	return false
}

// FindMatchingInterval uses (min, max]. Zero-usage records have no tokens to charge.
func pricingIntervalsCoverAllTokens(intervals []PricingInterval, priced func(*PricingInterval) bool) bool {
	end := 0
	sorted := slices.Clone(intervals)
	slices.SortFunc(sorted, func(a, b PricingInterval) int { return cmp.Compare(a.MinTokens, b.MinTokens) })
	for _, interval := range sorted {
		if interval.MinTokens != end || !priced(&interval) {
			return false
		}
		if interval.MaxTokens == nil {
			return true
		}
		if *interval.MaxTokens <= end {
			return false
		}
		end = *interval.MaxTokens
	}
	return false
}

func (r *ModelPricingResolver) hasUsablePrice(ctx context.Context, apiKey *APIKey, account *Account, model string) bool {
	if strings.TrimSpace(model) == "" {
		return false
	}
	input := PricingInput{Model: model}
	if apiKey != nil && apiKey.Group != nil {
		input.Group = apiKey.Group
		if input.Group.ID > 0 {
			input.GroupID = &input.Group.ID
		}
	}
	longContextEnabled := input.Group == nil || input.Group.LongContextPricingEnabled
	if gate := openAILongContextBillingGate(account); gate != nil && *gate {
		longContextEnabled = true
	}
	return r.Resolve(ctx, input).hasUsablePrice(longContextEnabled)
}

type forwardPricingCheck func(ctx context.Context, account *Account, model, upstreamModel, billingModel string) error
type forwardPricingCheckKey struct{}

func checkForwardModelPricing(ctx context.Context, account *Account, model, upstreamModel, billingModel string) error {
	check, _ := ctx.Value(forwardPricingCheckKey{}).(forwardPricingCheck)
	if check == nil {
		return nil
	}
	return check(ctx, account, model, upstreamModel, billingModel)
}

// WithModelPricingAdmission attaches the authenticated billing group and the
// same channel mapping used for settlement to forwarding calls for this request.
// The forwarder calls the check once it knows the actual outbound model.
func (s *GatewayService) WithModelPricingAdmission(ctx context.Context, key *APIKey, fields ChannelUsageFields) context.Context {
	if key == nil || (s.cfg != nil && s.cfg.RunMode == config.RunModeSimple) {
		return ctx
	}
	return context.WithValue(ctx, forwardPricingCheckKey{}, forwardPricingCheck(func(ctx context.Context, account *Account, model, upstreamModel, _ string) error {
		if s == nil || s.resolver == nil || s.billingService == nil {
			return rejectMissingModelPrice(key, account, model, upstreamModel)
		}
		pricingAt, _ := gatewayTokenRequestPricingAtFromContext(ctx)
		multiplier, _, _ := s.recordUsageMultipliers(ctx, key, key.User, pricingAt)
		if multiplier == 0 {
			return nil
		}
		result := &ForwardResult{Model: model, UpstreamModel: upstreamModel}
		billingModel := s.recordUsageBillingModel(ctx, key, result, fields)
		if s.resolver.hasUsablePrice(ctx, key, nil, billingModel) {
			return nil
		}
		return rejectMissingModelPrice(key, account, billingModel, upstreamModel)
	}))
}

func (s *OpenAIGatewayService) WithModelPricingAdmission(ctx context.Context, key *APIKey, fields ChannelUsageFields) context.Context {
	if key == nil || (s.cfg != nil && s.cfg.RunMode == config.RunModeSimple) {
		return ctx
	}
	return context.WithValue(ctx, forwardPricingCheckKey{}, forwardPricingCheck(func(ctx context.Context, account *Account, model, upstreamModel, billingModel string) error {
		return s.CheckModelPricingAdmission(ctx, key, account, fields, model, upstreamModel, billingModel)
	}))
}

func (s *OpenAIGatewayService) CheckModelPricingAdmission(ctx context.Context, key *APIKey, account *Account, fields ChannelUsageFields, model, upstreamModel, billingModel string) error {
	if key == nil || (s != nil && s.cfg != nil && s.cfg.RunMode == config.RunModeSimple) {
		return nil
	}
	if s == nil || s.resolver == nil || s.billingService == nil {
		return rejectMissingModelPrice(key, account, model, upstreamModel)
	}
	if key.Group != nil && key.User != nil && s.ResolveUserGroupRateMultiplier(ctx, key.User.ID, key.Group.ID, key.Group.RateMultiplier) == 0 {
		return nil
	}
	for _, candidate := range s.openAIUsageBillingModels(ctx, key, account, model, upstreamModel, billingModel, fields) {
		if s.resolver.hasUsablePrice(ctx, key, account, candidate) {
			return nil
		}
	}
	return rejectMissingModelPrice(key, account, model, upstreamModel)
}

func rejectMissingModelPrice(key *APIKey, account *Account, model, upstreamModel string) error {
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	slog.Warn("gateway_model_pricing_unavailable", "group_id", derefGroupID(key.GroupID), "account_id", accountID,
		"billing_model", model, "upstream_model", upstreamModel)
	return fmt.Errorf("%w for model %q", ErrModelPricingUnavailable, model)
}
