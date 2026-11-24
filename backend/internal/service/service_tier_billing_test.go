package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveBillingServiceTier(t *testing.T) {
	tests := []struct {
		name       string
		requested  string
		observed   string
		billing    string
		downgraded bool
	}{
		{name: "openai priority served as default", requested: "priority", observed: "default", billing: "default", downgraded: true},
		{name: "anthropic fast served as standard", requested: "fast", observed: "standard", billing: "standard", downgraded: true},
		{name: "ultrafast served as priority", requested: "ultrafast", observed: "priority", billing: "priority", downgraded: true},
		{name: "ultrafast served as default", requested: "ultrafast", observed: "default", billing: "default", downgraded: true},
		{name: "ultrafast honoured", requested: "ultrafast", observed: "ultrafast", billing: "ultrafast"},
		{name: "ultrafast missing declaration", requested: "ultrafast", billing: "ultrafast"},
		{name: "response cannot upgrade to ultrafast", requested: "priority", observed: "ultrafast", billing: "priority"},
		{name: "priority honoured", requested: "priority", observed: "priority", billing: "priority"},
		{name: "no declaration keeps request", requested: "priority", observed: "", billing: "priority"},
		{name: "no request no declaration", requested: "", observed: "", billing: ""},
		{name: "response never raises the tier", requested: "", observed: "priority", billing: ""},
		{name: "flex never raised to default", requested: "flex", observed: "default", billing: "flex"},
		{name: "default echoed for untiered request", requested: "", observed: "default", billing: ""},
		{name: "unknown response tier ignored", requested: "priority", observed: "turbo", billing: "priority"},
		{name: "case and whitespace normalised", requested: " Priority ", observed: "DEFAULT", billing: "default", downgraded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveBillingServiceTier(tt.requested, tt.observed)
			require.Equal(t, tt.billing, got.Billing)
			require.Equal(t, tt.downgraded, got.Downgraded)
		})
	}
}

func TestResolveUpstreamReportedServiceTier(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		observed  string
		billing   string
	}{
		{name: "fast confirmed", requested: "priority", observed: "priority", billing: "priority"},
		{name: "fast alias normalized", requested: "default", observed: " FAST ", billing: "priority"},
		{name: "fast downgraded by upstream", requested: "priority", observed: "default", billing: "default"},
		{name: "upstream upgrades request", requested: "default", observed: "priority", billing: "priority"},
		{name: "missing response uses base", requested: "priority", observed: "", billing: "default"},
		{name: "unknown response uses base", requested: "priority", observed: "turbo", billing: "default"},
		{name: "standard alias uses base", requested: "priority", observed: "standard", billing: "default"},
		{name: "auto is no processing confirmation", requested: "priority", observed: "auto", billing: "default"},
		{name: "explicit flex retained", requested: "priority", observed: "flex", billing: "flex"},
		{name: "explicit ultrafast retained", requested: "ultrafast", observed: "ultrafast", billing: "ultrafast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveUpstreamReportedServiceTier(tt.requested, tt.observed)
			require.Equal(t, tt.billing, got.Billing)
		})
	}
}

func TestResolveOpenAIServiceTierBillingForAccountCost(t *testing.T) {
	t.Run("codex exception only covers OpenAI default", func(t *testing.T) {
		require.True(t, codexOAuthResponseTierIsNonAuthoritative("default"))
		require.False(t, codexOAuthResponseTierIsNonAuthoritative("standard"))
		require.False(t, codexOAuthResponseTierIsNonAuthoritative("flex"))
	})

	t.Run("openai API estimates the lower tier", func(t *testing.T) {
		resolution := ResolveOpenAIServiceTierBilling(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "priority", "default")
		require.True(t, resolution.Downgraded)
		require.Equal(t, "default", resolution.Billing)
	})

	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run("codex "+accountType+" keeps outbound priority despite default echo", func(t *testing.T) {
			resolution := ResolveOpenAIServiceTierBilling(&Account{Platform: PlatformOpenAI, Type: accountType}, "priority", "default")
			require.False(t, resolution.Downgraded)
			require.Equal(t, "priority", resolution.Requested)
			require.Equal(t, "default", resolution.Observed)
			require.Equal(t, "priority", resolution.Billing)
		})

		t.Run("codex "+accountType+" still accepts an explicit flex downgrade", func(t *testing.T) {
			resolution := ResolveOpenAIServiceTierBilling(&Account{Platform: PlatformOpenAI, Type: accountType}, "priority", "flex")
			require.True(t, resolution.Downgraded)
			require.Equal(t, "flex", resolution.Billing)
		})

		t.Run("codex "+accountType+" response never promotes an untiered request", func(t *testing.T) {
			resolution := ResolveOpenAIServiceTierBilling(&Account{Platform: PlatformOpenAI, Type: accountType}, "", "priority")
			require.False(t, resolution.Downgraded)
			require.Empty(t, resolution.Billing)
		})
	}

	t.Run("non-openai oauth still uses the generic response contract", func(t *testing.T) {
		resolution := ResolveOpenAIServiceTierBilling(&Account{Platform: PlatformGrok, Type: AccountTypeOAuth}, "priority", "default")
		require.True(t, resolution.Downgraded)
		require.Equal(t, "default", resolution.Billing)
	})
}

func TestApplyForwardServiceTierBillingResolution(t *testing.T) {
	t.Run("anthropic standard speed rewrites fast", func(t *testing.T) {
		requested := "fast"
		result := &ForwardResult{ServiceTier: &requested, UpstreamResponseServiceTier: "standard"}
		require.True(t, ApplyForwardServiceTierBillingResolution(result).Downgraded)
		require.Equal(t, "standard", *result.ServiceTier)
	})

	t.Run("nil results are ignored", func(t *testing.T) {
		require.False(t, ApplyForwardServiceTierBillingResolution(nil).Downgraded)
	})
}
