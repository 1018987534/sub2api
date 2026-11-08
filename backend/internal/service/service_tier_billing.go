package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// ServiceTierBillingResolution describes how the billable service tier of one
// request was settled between the tier the client asked for and the tier the
// upstream reports having used.
type ServiceTierBillingResolution struct {
	Requested  string // tier carried by the request sent upstream ("" when none)
	Observed   string // tier declared by the upstream response ("" when none)
	Billing    string // tier used for billing and the usage log
	Downgraded bool   // Billing is cheaper than Requested
}

// ResolveBillingServiceTier picks the tier to bill. The upstream declaration is
// trusted only to lower the bill: a request served below the tier it asked for
// (OpenAI "priority" answered with service_tier "default", Anthropic speed=fast
// answered with usage.speed "standard") is billed at the cheaper tier. A response
// that claims a more expensive tier, an unknown one, or none at all leaves the
// requested tier in place, which is also the behaviour for upstreams that never
// declare a tier.
func ResolveBillingServiceTier(requested, observed string) ServiceTierBillingResolution {
	requested = normalizeBillingServiceTier(requested)
	observed = normalizeBillingServiceTier(observed)
	resolution := ServiceTierBillingResolution{Requested: requested, Observed: observed, Billing: requested}
	if observed == "" || observed == requested {
		return resolution
	}
	observedRank, known := serviceTierCostRank(observed)
	if !known {
		return resolution
	}
	requestedRank, _ := serviceTierCostRank(requested)
	if observedRank >= requestedRank {
		return resolution
	}
	resolution.Billing = observed
	resolution.Downgraded = true
	return resolution
}

// serviceTierCostRank orders tiers by their cost relative to the base rate, so a
// lower rank is always cheaper. Unknown tiers rank as the base rate and report
// known=false so callers can refuse to act on them.
func serviceTierCostRank(tier string) (rank int, known bool) {
	switch normalizeBillingServiceTier(tier) {
	case "flex":
		return 0, true
	case "", "default", "standard", "auto", "scale":
		return 1, true
	case "priority", "fast":
		return 2, true
	case "ultrafast":
		return 3, true
	default:
		return 1, false
	}
}

// resolveUpstreamReportedServiceTier uses a recognized upstream response tier.
// Missing or unknown declarations select Standard instead of inferring Fast
// from the outbound request. The request tier remains available for auditing.
func resolveUpstreamReportedServiceTier(requested, observed string) ServiceTierBillingResolution {
	requested = normalizeBillingServiceTier(requested)
	observed = normalizeBillingServiceTier(observed)
	billing := "default"
	switch observed {
	case "priority", "fast":
		billing = OpenAIFastTierPriority
	case "flex", "ultrafast":
		billing = observed
	}
	requestedRank, _ := serviceTierCostRank(requested)
	billingRank, _ := serviceTierCostRank(billing)
	return ServiceTierBillingResolution{
		Requested:  requested,
		Observed:   observed,
		Billing:    billing,
		Downgraded: billingRank < requestedRank,
	}
}

// ResolveOpenAIServiceTierBilling applies the existing response-tier contract
// for the selected credential. Public OpenAI API responses may lower billing.
// The private ChatGPT Codex endpoint keeps its established default-echo
// exception here; the opt-in global reconciliation mode uses the stricter
// resolveUpstreamReportedServiceTier path above.
func ResolveOpenAIServiceTierBilling(account *Account, requested, observed string) ServiceTierBillingResolution {
	if account != nil && account.IsOpenAIOAuthLike() && codexOAuthResponseTierIsNonAuthoritative(observed) {
		return ServiceTierBillingResolution{
			Requested: normalizeBillingServiceTier(requested),
			Observed:  normalizeBillingServiceTier(observed),
			Billing:   normalizeBillingServiceTier(requested),
		}
	}
	return ResolveBillingServiceTier(requested, observed)
}

func codexOAuthResponseTierIsNonAuthoritative(observed string) bool {
	switch normalizeBillingServiceTier(observed) {
	case "default":
		return true
	default:
		return false
	}
}

// ApplyOpenAIServiceTierBillingResolution lowers result.ServiceTier only when
// the selected credential's upstream response tier is authoritative.
func ApplyOpenAIServiceTierBillingResolution(account *Account, result *OpenAIForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	resolution := ResolveOpenAIServiceTierBilling(account, optionalStringValue(result.ServiceTier), result.UpstreamResponseServiceTier)
	if resolution.Downgraded {
		billing := resolution.Billing
		result.ServiceTier = &billing
	}
	return resolution
}

// resolveServiceTierBilling applies the global billing policy before calculating
// charges and persisting the same service tier for usage display.
func (s *OpenAIGatewayService) resolveServiceTierBilling(ctx context.Context, account *Account, result *OpenAIForwardResult) (ServiceTierBillingResolution, error) {
	if result == nil || account == nil || !account.IsOpenAI() {
		return ApplyOpenAIServiceTierBillingResolution(account, result), nil
	}
	settings := openAIFastPolicySettingsFromContext(ctx)
	if settings == nil && s.settingService != nil {
		var err error
		settings, err = s.settingService.GetOpenAIFastPolicySettings(ctx)
		if err != nil {
			return ServiceTierBillingResolution{}, fmt.Errorf("get service tier billing policy: %w", err)
		}
	}
	if settings != nil && settings.BillingFollowUpstreamTier {
		resolution := resolveUpstreamReportedServiceTier(
			optionalStringValue(result.ServiceTier),
			result.UpstreamResponseServiceTier,
		)
		billing := resolution.Billing
		result.ServiceTier = &billing
		logServiceTierBillingResolution(ctx, account, result, resolution)
		return resolution, nil
	}
	return ApplyOpenAIServiceTierBillingResolution(account, result), nil
}

// ApplyForwardServiceTierBillingResolution is the ForwardResult counterpart of
// ApplyOpenAIServiceTierBillingResolution.
func ApplyForwardServiceTierBillingResolution(result *ForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	resolution := ResolveBillingServiceTier(optionalStringValue(result.ServiceTier), result.UpstreamResponseServiceTier)
	if resolution.Downgraded {
		billing := resolution.Billing
		result.ServiceTier = &billing
	}
	return resolution
}

// logServiceTierBillingDowngrade leaves an audit trail for every request billed
// below the tier it asked for; unchanged tiers are not logged.
func logServiceTierBillingDowngrade(component string, account *Account, requestID string, resolution ServiceTierBillingResolution) {
	if !resolution.Downgraded {
		return
	}
	attrs := []any{
		"component", component,
		"request_id", strings.TrimSpace(requestID),
		"requested_tier", resolution.Requested,
		"response_tier", resolution.Observed,
		"billed_tier", resolution.Billing,
	}
	if account != nil {
		attrs = append(attrs, "platform", account.Platform, "account_id", account.ID)
	}
	slog.Info("billing.service_tier_downgraded", attrs...)
}
