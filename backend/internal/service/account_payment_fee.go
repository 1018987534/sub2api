package service

import (
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// UpstreamPaymentFeePercentExtraKey stores the surcharge paid on upstream purchases.
// The stored account rate and probe snapshots remain before fees so edits and
// repeated probes cannot compound the surcharge.
const UpstreamPaymentFeePercentExtraKey = "upstream_payment_fee_percent"

func validUpstreamPaymentFeePercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func ValidateUpstreamPaymentFeeExtra(extra map[string]any) error {
	if _, exists := extra[UpstreamPaymentFeePercentExtraKey]; !exists {
		return nil
	}
	value, ok := resolveAccountExtraNumber(extra, UpstreamPaymentFeePercentExtraKey)
	if _, isString := extra[UpstreamPaymentFeePercentExtraKey].(string); !ok || isString || !validUpstreamPaymentFeePercent(value) {
		return infraerrors.BadRequest("INVALID_UPSTREAM_PAYMENT_FEE_PERCENT", "upstream payment fee percent must be finite and between 0 and 100")
	}
	return nil
}

func (a *Account) UpstreamPaymentFeePercent() float64 {
	if a == nil {
		return 0
	}
	value, ok := resolveAccountExtraNumber(a.Extra, UpstreamPaymentFeePercentExtraKey)
	if _, isString := a.Extra[UpstreamPaymentFeePercentExtraKey].(string); !ok || isString || !validUpstreamPaymentFeePercent(value) {
		return 0
	}
	return value
}

func (a *Account) ApplyUpstreamPaymentFee(rate float64) float64 {
	fee := a.UpstreamPaymentFeePercent()
	if fee == 0 {
		return rate
	}
	return math.Round(rate*(1+fee/100)*1e12) / 1e12
}
