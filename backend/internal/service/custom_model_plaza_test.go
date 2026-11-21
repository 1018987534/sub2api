//go:build unit

package service

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type customPlazaSettingsStub struct {
	SettingRepository
	values map[string]string
}

func (s customPlazaSettingsStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s customPlazaSettingsStub) GetValuesByPrefixes(_ context.Context, prefixes ...string) (map[string]string, error) {
	out := make(map[string]string)
	for k, v := range s.values {
		for _, prefix := range prefixes {
			if strings.HasPrefix(k, prefix) {
				out[k] = v
			}
		}
	}
	return out, nil
}

func (s customPlazaSettingsStub) CompareAndSetValue(_ context.Context, key string, before, after *string) (bool, error) {
	if current, exists := s.values[key]; exists != (before != nil) || exists && current != *before {
		return false, nil
	}
	if after == nil {
		delete(s.values, key)
	} else {
		s.values[key] = *after
	}
	return true, nil
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

func TestCustomModelPlazaCatalogCRUD(t *testing.T) {
	ctx := context.Background()
	const starter = "claude-opus-5-5"
	legacy := `[{"min_tokens":0,"max_tokens":0,"input":11}]`
	settings := customPlazaSettingsStub{values: map[string]string{
		SettingKeyModelPlazaEnabled: "false", customPlazaSettingPrefix + starter: legacy,
	}}
	billing := newTestBillingService()
	newService := func() *CustomModelPlazaService { return NewCustomModelPlazaService(settings, billing) }
	plaza := newService()
	initial, err := plaza.List(ctx)
	require.NoError(t, err)
	seed, err := plaza.GetAdminModel(ctx, starter)
	require.NoError(t, err)
	require.Equal(t, 11.0, *seed.Overrides[0].Input)

	const model = "acme/claude-opus-experimental:online"
	quote, err := plaza.PreviewDefaults(ctx, model, "Research")
	require.NoError(t, err)
	require.Nil(t, quote.DefaultTiers[0].Input, "unknown ID must not inherit a guessed Opus price")
	input, free := 2.5, 0.0
	config := CustomPlazaConfig{ID: model, Family: "Research", ContextLimits: []int{42000}, Tiers: []CustomPlazaTier{
		{MaxTokens: 42000, CustomPlazaRates: CustomPlazaRates{Input: &input}},
		{MinTokens: 42000, CustomPlazaRates: CustomPlazaRates{CacheRead: &free}},
	}}
	created, err := plaza.Configure(ctx, config, true)
	require.NoError(t, err)
	require.Equal(t, config.Tiers, created.Overrides)

	plaza = newService() // The catalog is shared storage, not process memory.
	public, err := plaza.List(ctx)
	require.NoError(t, err)
	require.Len(t, public, len(initial)+1)
	require.Equal(t, config.Tiers, public[len(public)-1].Tiers)
	duplicate := config
	duplicate.ID = strings.ToUpper(config.ID)
	_, err = plaza.Configure(ctx, duplicate, true)
	require.ErrorIs(t, err, errCustomPlazaDuplicate)

	patch := CustomPlazaPricePatch{ID: model, MaxTokens: 42000, Rates: map[string]*float64{"input": nil, "output": &input}}
	updatedPrices, err := plaza.PatchPrices(ctx, patch)
	require.NoError(t, err)
	require.Nil(t, updatedPrices.Overrides[0].Input)
	require.Equal(t, input, *updatedPrices.Overrides[0].Output)
	require.Equal(t, free, *updatedPrices.Overrides[1].CacheRead, "the second tier must remain unchanged")
	patch.Rates = map[string]*float64{"billing_multiplier": &input}
	_, err = plaza.PatchPrices(ctx, patch)
	require.Equal(t, "CUSTOM_PLAZA_INVALID_PRICE", infraerrors.Reason(err))
	_, err = plaza.PatchPrices(ctx, CustomPlazaPricePatch{ID: "gpt-6.2-astra", MaxTokens: 272000, Rates: map[string]*float64{"cache_write": &input}})
	require.Equal(t, "CUSTOM_PLAZA_GPT_CACHE_WRITE", infraerrors.Reason(err))

	config.ContextLimits, config.Tiers = []int{42000, 40000}, nil
	_, err = plaza.Configure(ctx, config, false)
	require.Equal(t, "CUSTOM_PLAZA_INVALID_LIMITS", infraerrors.Reason(err))
	config.ContextLimits, config.Tiers, config.Family, config.Hidden = []int{}, []CustomPlazaTier{}, "新系列", true
	_, err = plaza.Configure(ctx, config, false)
	require.NoError(t, err)
	updated, err := plaza.GetAdminModel(ctx, model)
	require.NoError(t, err)
	require.True(t, updated.Hidden)
	require.Equal(t, "新系列", updated.Family)
	require.NotNil(t, updated.ContextLimits, "a manual single tier is distinct from following official limits")
	public, err = plaza.List(ctx)
	require.NoError(t, err)
	require.Equal(t, initial, public)

	require.NoError(t, plaza.DeleteModel(ctx, model))
	_, err = plaza.GetAdminModel(ctx, model)
	require.ErrorIs(t, err, errCustomPlazaModelNotFound)
	require.NotContains(t, settings.values, plazaRecordKey(model))
	require.NoError(t, plaza.DeleteModel(ctx, starter))
	_, err = newService().GetAdminModel(ctx, starter)
	require.ErrorIs(t, err, errCustomPlazaModelNotFound)
	restored, err := plaza.Configure(ctx, CustomPlazaConfig{ID: starter, Family: "claude"}, true)
	require.NoError(t, err)
	require.Empty(t, restored.Overrides, "recreating a removed model must not restore old overrides")
	require.Equal(t, legacy, settings.values[customPlazaSettingPrefix+starter], "legacy settings remain available to an older binary")
	require.Equal(t, "false", settings.values[SettingKeyModelPlazaEnabled])
}
