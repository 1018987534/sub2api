package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountPaymentFeeRoundTripKeepsBaseRate(t *testing.T) {
	base := 0.1
	a := &service.Account{RateMultiplier: &base, Extra: map[string]any{service.UpstreamPaymentFeePercentExtraKey: 3.0}}
	response := AccountFromServiceShallow(a)
	require.Equal(t, 0.1, response.RateMultiplier)
	require.Equal(t, 3.0, response.Extra[service.UpstreamPaymentFeePercentExtraKey])
	// An unchanged form round trip must not apply a second surcharge.
	roundTrip := &service.Account{RateMultiplier: &response.RateMultiplier, Extra: response.Extra}
	require.Equal(t, 0.103, roundTrip.BillingRateMultiplier())
}
