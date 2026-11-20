//go:build unit

package service

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

type customPlazaSettingsStub struct {
	SettingRepository
	values map[string]string
}

func (s customPlazaSettingsStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, key := range keys {
		out[key] = s.values[key]
	}
	return out, nil
}

func (s customPlazaSettingsStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func (s customPlazaSettingsStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func TestCustomModelPlazaOverridesDoNotChangeBilling(t *testing.T) {
	ctx := context.Background()
	settings := customPlazaSettingsStub{values: map[string]string{SettingKeyModelPlazaEnabled: "false"}}
	billing := newTestBillingService()
	plaza := NewCustomModelPlazaService(settings, billing)
	list := func() map[string]CustomPlazaModel {
		t.Helper()
		models, err := plaza.List(ctx)
		require.NoError(t, err)
		out := make(map[string]CustomPlazaModel, len(models))
		families := map[string]bool{}
		for _, model := range models {
			require.NotContains(t, out, model.ID)
			out[model.ID] = model
			families[model.Family] = true
		}
		require.ElementsMatch(t, []string{"gpt", "claude", "kimi", "glm", "deepseek"}, slices.Collect(maps.Keys(families)))
		return out
	}
	const model = "gpt-6.2-astra"
	native, err := billing.GetModelPricing(model)
	require.NoError(t, err)
	require.Positive(t, native.CacheCreationPricePerToken)
	tokens := UsageTokens{InputTokens: 270_000, OutputTokens: 200, CacheCreationTokens: 9_000}
	before, err := billing.CalculateCost(model, tokens, 1)
	require.NoError(t, err)
	defaults := list()
	gpt := defaults[model].Tiers
	require.Len(t, gpt, 2)
	require.Equal(t, 272_000, gpt[0].MaxTokens)
	require.Equal(t, 272_000, gpt[1].MinTokens)
	require.Equal(t, 8.0, *gpt[0].Input)
	require.Equal(t, 16.0, *gpt[1].Input)
	require.Equal(t, 0.8, *gpt[1].CacheRead)
	for _, tier := range gpt {
		require.Zero(t, *tier.CacheWrite)
		require.Zero(t, *tier.CacheWrite1h)
		require.Zero(t, *tier.CacheWrite12h)
	}
	claude := defaults["claude-opus-5-5"].Tiers[0]
	require.Equal(t, 4.0, *claude.Input)
	require.Equal(t, 5.0, *claude.CacheWrite)
	require.Equal(t, 8.0, *claude.CacheWrite1h)

	newInput, zero := 12.75, 0.0
	override := CustomPlazaTier{MaxTokens: 272_000, CustomPlazaRates: CustomPlazaRates{Input: &newInput, CacheRead: &zero}}
	entry, err := plaza.SetOverrides(ctx, model, []CustomPlazaTier{override})
	require.NoError(t, err)
	require.Equal(t, []CustomPlazaTier{override}, entry.Overrides)
	modified := list()[model].Tiers
	require.Equal(t, newInput, *modified[0].Input)
	require.Equal(t, zero, *modified[0].CacheRead)
	require.Equal(t, gpt[0].Output, modified[0].Output)
	require.Equal(t, gpt[1], modified[1])
	after, err := billing.CalculateCost(model, tokens, 1)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, "false", settings.values[SettingKeyModelPlazaEnabled])
	_, err = plaza.SetOverrides(ctx, model, nil)
	require.NoError(t, err)
	require.Equal(t, gpt, list()[model].Tiers)
}

func TestCustomModelPlazaRejectsInvalidOverrides(t *testing.T) {
	settings := customPlazaSettingsStub{values: map[string]string{}}
	plaza := NewCustomModelPlazaService(settings, newTestBillingService())
	negative, zero := -1.0, 0.0
	cases := []struct {
		id    string
		tiers []CustomPlazaTier
	}{
		{"not-a-published-model", []CustomPlazaTier{}},
		{"claude-opus-5-5", []CustomPlazaTier{{MaxTokens: 17}}},
		{"claude-opus-5-5", []CustomPlazaTier{{}, {}}},
		{"claude-opus-5-5", []CustomPlazaTier{{CustomPlazaRates: CustomPlazaRates{Input: &negative}}}},
		{"gpt-6.2-astra", []CustomPlazaTier{{MinTokens: 272_000, CustomPlazaRates: CustomPlazaRates{CacheWrite: &zero}}}},
	}
	for _, tc := range cases {
		_, err := plaza.SetOverrides(context.Background(), tc.id, tc.tiers)
		require.Error(t, err)
	}
	require.Empty(t, settings.values)
}

func TestCustomModelPlazaFollowsIdentifiedDirectoryAndDistinguishesZero(t *testing.T) {
	settings := customPlazaSettingsStub{values: map[string]string{}}
	billing := newTestBillingService()
	plaza := NewCustomModelPlazaService(settings, billing)
	findPrices := func(id string) CustomPlazaRates {
		t.Helper()
		models, err := plaza.List(context.Background())
		require.NoError(t, err)
		for _, model := range models {
			if model.ID == id {
				return model.Tiers[0].CustomPlazaRates
			}
		}
		t.Fatalf("model not found: %s", id)
		return CustomPlazaRates{}
	}
	const model = "deepseek-v4.1-flash"
	initial := findPrices(model)
	require.Equal(t, 2.0, *initial.Input)
	require.Nil(t, initial.CacheWrite)
	billing.pricingService = newStubPricingServiceFromJSON(t, `{"deepseek-v4.1-flash":{"input_cost_per_token":0.000003,"output_cost_per_token":0.000009,"cache_creation_input_token_cost":0}}`)
	configured := findPrices(model)
	require.Equal(t, 3.0, *configured.Input)
	require.Equal(t, 9.0, *configured.Output)
	require.NotNil(t, configured.CacheWrite)
	require.Zero(t, *configured.CacheWrite)
	billed, err := billing.GetModelPricing(model)
	require.NoError(t, err)
	require.NotEqual(t, *configured.Input, *plazaPerMillion(&billed.InputPricePerToken))
}
