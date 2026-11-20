package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const customPlazaSettingPrefix = "custom_model_plaza_price:"

var errCustomPlazaModelNotFound = infraerrors.NotFound("CUSTOM_PLAZA_MODEL_NOT_FOUND", "model is not in the custom plaza")

// All prices here are display-only, in USD per million tokens. Nil is unconfigured,
// not zero. The settings below are never read by billing or channel resolution.
type CustomPlazaRates struct {
	Input         *float64 `json:"input"`
	Output        *float64 `json:"output"`
	CacheRead     *float64 `json:"cache_read"`
	CacheWrite    *float64 `json:"cache_write"`
	CacheWrite1h  *float64 `json:"cache_write_1h"`
	CacheWrite12h *float64 `json:"cache_write_12h"`
}

type CustomPlazaTier struct {
	// (MinTokens, MaxTokens], or MaxTokens == 0 for the final unbounded tier.
	MinTokens int `json:"min_tokens"`
	MaxTokens int `json:"max_tokens"`
	CustomPlazaRates
}

func (t CustomPlazaTier) bounds() [2]int { return [2]int{t.MinTokens, t.MaxTokens} }

type CustomPlazaModel struct {
	ID     string            `json:"id"`
	Family string            `json:"family"`
	Tiers  []CustomPlazaTier `json:"tiers"`
}

type CustomPlazaAdminModel struct {
	ID           string            `json:"id"`
	Family       string            `json:"family"`
	DefaultTiers []CustomPlazaTier `json:"default_tiers"`
	Overrides    []CustomPlazaTier `json:"overrides"`
}

type customPlazaCatalogEntry struct {
	id          string
	priceModel  string
	fallbackUSD *[3]float64 // input, output, cache hit; for SKUs that may be absent in the directory
}

func plazaModels(ids ...string) []customPlazaCatalogEntry {
	entries := make([]customPlazaCatalogEntry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, customPlazaCatalogEntry{id: id, priceModel: id})
	}
	return entries
}

// Curated display inventory, in publication order. Channel entitlement, account
// availability and user-specific rates must not affect this catalog.
var customPlazaCatalog = []struct {
	family string
	models []customPlazaCatalogEntry
}{
	{"gpt", append(append(plazaModels("gpt-6.2-astra", "gpt-6.2-sol", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-astra"),
		customPlazaCatalogEntry{id: "gpt-6", priceModel: "gpt-6-astra"}),
		plazaModels("gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "codex-auto-review")...)},
	{"claude", plazaModels("claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-2", "claude-fable-5-1", "claude-fable-5",
		"claude-mythos-5-1", "claude-mythos-5", "claude-mythos-preview", "claude-opus-5", "claude-sonnet-5", "claude-opus-4-8", "claude-opus-4-7",
		"claude-opus-4-6", "claude-opus-4-6-thinking", "claude-sonnet-4-6", "claude-opus-4-5", "claude-opus-4-5-20251101", "claude-opus-4-1",
		"claude-sonnet-4-5", "claude-haiku-4-5", "claude-haiku-4-5-20251001", "claude-3-5-haiku-20241022")},
	{"kimi", append([]customPlazaCatalogEntry{
		{id: "kimi-k3.1-pro", priceModel: "kimi-k3.1-pro", fallbackUSD: &[3]float64{2, 10, 0.2}},
		{id: "kimi-k3.1-pro-highspeed", priceModel: "kimi-k3.1-pro-highspeed", fallbackUSD: &[3]float64{4, 20, 0.2}},
	}, plazaModels("kimi-k3", "kimi-k2.7-code", "kimi-k2.6")...)},
	{"glm", plazaModels("glm-5.3", "glm-5.3-flash", "glm-5.2", "glm-5.1", "glm-5-turbo", "glm-5")},
	{"deepseek", append([]customPlazaCatalogEntry{
		// This SKU is listed in the reference plaza but is not yet in the default directory.
		{id: "deepseek-v4.1-flash", priceModel: "deepseek-v4.1-flash", fallbackUSD: &[3]float64{2, 8, 0.04}},
	}, plazaModels("deepseek-v4-pro", "deepseek-v4-flash", "deepseek-flash")...)},
}

type CustomModelPlazaService struct {
	settings SettingRepository
	billing  *BillingService
	resolver *ModelPricingResolver
}

func NewCustomModelPlazaService(settings SettingRepository, billing *BillingService) *CustomModelPlazaService {
	return &CustomModelPlazaService{settings: settings, billing: billing, resolver: NewModelPricingResolver(nil, billing)}
}

func (s *CustomModelPlazaService) List(ctx context.Context) ([]CustomPlazaModel, error) {
	admin, err := s.ListAdmin(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]CustomPlazaModel, 0, len(admin))
	for _, m := range admin {
		for i := range m.DefaultTiers {
			for _, override := range m.Overrides {
				if override.bounds() == m.DefaultTiers[i].bounds() {
					m.DefaultTiers[i].CustomPlazaRates = overlayPlazaRates(m.DefaultTiers[i].CustomPlazaRates, override.CustomPlazaRates)
					break
				}
			}
			if m.Family == "gpt" {
				m.DefaultTiers[i].CustomPlazaRates = freeGPTPlazaCacheWrites(m.DefaultTiers[i].CustomPlazaRates)
			}
		}
		models = append(models, CustomPlazaModel{ID: m.ID, Family: m.Family, Tiers: m.DefaultTiers})
	}
	return models, nil
}

func (s *CustomModelPlazaService) ListAdmin(ctx context.Context) ([]CustomPlazaAdminModel, error) {
	var keys []string
	for _, section := range customPlazaCatalog {
		for _, model := range section.models {
			keys = append(keys, customPlazaSettingPrefix+model.id)
		}
	}
	values, err := s.settings.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("read custom plaza price settings: %w", err)
	}
	models := make([]CustomPlazaAdminModel, 0, len(keys))
	for _, section := range customPlazaCatalog {
		for _, model := range section.models {
			result := CustomPlazaAdminModel{
				ID: model.id, Family: section.family, Overrides: []CustomPlazaTier{},
				DefaultTiers: s.defaultTiers(ctx, section.family, model),
			}
			if raw := values[customPlazaSettingPrefix+model.id]; raw != "" {
				if err := json.Unmarshal([]byte(raw), &result.Overrides); err != nil {
					return nil, fmt.Errorf("decode custom plaza price for %s: %w", model.id, err)
				}
			}
			models = append(models, result)
		}
	}
	return models, nil
}

func (s *CustomModelPlazaService) SetOverrides(ctx context.Context, id string, overrides []CustomPlazaTier) (*CustomPlazaAdminModel, error) {
	for _, section := range customPlazaCatalog {
		for _, model := range section.models {
			if model.id != id {
				continue
			}
			result := &CustomPlazaAdminModel{ID: id, Family: section.family, DefaultTiers: s.defaultTiers(ctx, section.family, model), Overrides: []CustomPlazaTier{}}
			known, seen := map[[2]int]bool{}, map[[2]int]bool{}
			for _, tier := range result.DefaultTiers {
				known[tier.bounds()] = true
			}
			for _, tier := range overrides {
				bounds := tier.bounds()
				if !known[bounds] || seen[bounds] {
					return nil, infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_TIER", "unknown or duplicate context tier")
				}
				seen[bounds] = true
				if section.family == "gpt" && (tier.CacheWrite != nil || tier.CacheWrite1h != nil || tier.CacheWrite12h != nil) {
					return nil, infraerrors.BadRequest("CUSTOM_PLAZA_GPT_CACHE_WRITE", "GPT cache write display prices are fixed at zero")
				}
				hasPrice := false
				for _, field := range tier.CustomPlazaRates.fields() {
					if *field != nil {
						v := **field
						if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1_000_000 {
							return nil, infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_PRICE", "prices must be between 0 and 1000000 USD per million tokens")
						}
						hasPrice = true
					}
				}
				if hasPrice {
					result.Overrides = append(result.Overrides, tier)
				}
			}
			key := customPlazaSettingPrefix + id
			if len(result.Overrides) == 0 {
				if err := s.settings.Delete(ctx, key); err != nil && !errors.Is(err, ErrSettingNotFound) {
					return nil, fmt.Errorf("reset custom plaza model: %w", err)
				}
			} else {
				data, err := json.Marshal(result.Overrides)
				if err != nil {
					return nil, err
				}
				if err := s.settings.Set(ctx, key, string(data)); err != nil {
					return nil, fmt.Errorf("save custom plaza model: %w", err)
				}
			}
			return result, nil
		}
	}
	return nil, errCustomPlazaModelNotFound
}

func (s *CustomModelPlazaService) defaultTiers(ctx context.Context, family string, model customPlazaCatalogEntry) []CustomPlazaTier {
	base := CustomPlazaRates{}
	price, err := s.billing.GetModelPricing(model.priceModel)
	official := s.billing.pricingService.GetIdentifiedModelPricing(model.priceModel)
	hasOfficialTokenPrice := official != nil && !official.TokenPricingAbsent
	// For these newer SKUs a family-level fallback is not an official quote.
	// Once an identified catalog entry arrives it replaces the reference price.
	if ref := model.fallbackUSD; ref != nil && (err != nil || !hasOfficialTokenPrice) {
		input, output, read := ref[0], ref[1], ref[2]
		base.Input, base.Output, base.CacheRead = &input, &output, &read
	} else if err == nil && price != nil {
		base = CustomPlazaRates{
			Input: plazaPerMillion(nonZeroPtr(price.InputPricePerToken)), Output: plazaPerMillion(nonZeroPtr(price.OutputPricePerToken)),
			CacheRead:     plazaPerMillion(nonZeroPtr(price.CacheReadPricePerToken)),
			CacheWrite:    plazaPerMillion(nonZeroPtr(price.CacheCreationPricePerToken)),
			CacheWrite12h: plazaPerMillion(price.CacheCreation12hPricePerToken),
		}
		if price.SupportsCacheBreakdown {
			base.CacheWrite1h = plazaPerMillion(nonZeroPtr(price.CacheCreation1hPrice))
		}
		if hasOfficialTokenPrice {
			// The public price card must not inherit settlement-only policies, such
			// as forcing DeepSeek onto the legacy off-peak rate.
			base.Input = plazaPerMillion(nonZeroPtr(official.InputCostPerToken))
			base.Output = plazaPerMillion(nonZeroPtr(official.OutputCostPerToken))
			base.CacheRead = plazaPerMillion(nonZeroPtr(official.CacheReadInputTokenCost))
			base.CacheWrite = plazaPerMillion(contextPricePtr(&official.CacheCreationInputTokenCost, official.CacheCreationInputTokenCostExplicit))
			base.CacheWrite1h = plazaPerMillion(nonZeroPtr(official.CacheCreationInputTokenCostAbove1hr))
		}
		// Same official, no-group context schedule as the native price catalog.
		if schedule, err := s.billing.ResolveContextPricingSchedule(ctx, s.resolver, ContextPricingScheduleInput{Model: model.priceModel}); err == nil && schedule != nil && len(schedule.Tiers) > 1 {
			tiers := make([]CustomPlazaTier, 0, len(schedule.Tiers))
			for _, source := range schedule.Tiers {
				tier := CustomPlazaTier{MinTokens: source.MinTokens, CustomPlazaRates: CustomPlazaRates{
					Input: plazaPerMillion(source.Input), Output: plazaPerMillion(source.Output), CacheRead: plazaPerMillion(source.CacheRead),
					CacheWrite: plazaPerMillion(source.CacheWrite), CacheWrite12h: plazaPerMillion(source.CacheWrite12h),
				}}
				if tier.CacheWrite == nil && base.CacheWrite != nil && *base.CacheWrite == 0 {
					tier.CacheWrite = base.CacheWrite
				}
				if price.SupportsCacheBreakdown || source.CacheWrite1h != nil && source.CacheWrite != nil && *source.CacheWrite1h != *source.CacheWrite {
					tier.CacheWrite1h = plazaPerMillion(source.CacheWrite1h)
				}
				if source.MaxTokens != nil {
					tier.MaxTokens = *source.MaxTokens
				}
				if family == "gpt" {
					tier.CustomPlazaRates = freeGPTPlazaCacheWrites(tier.CustomPlazaRates)
				}
				tiers = append(tiers, tier)
			}
			return tiers
		}
	}
	if family == "gpt" {
		base = freeGPTPlazaCacheWrites(base)
	}
	return []CustomPlazaTier{{CustomPlazaRates: base}}
}

func plazaPerMillion(perToken *float64) *float64 {
	if perToken == nil {
		return nil
	}
	value := roundContextPrice(*perToken * 1_000_000)
	return &value
}

func freeGPTPlazaCacheWrites(rates CustomPlazaRates) CustomPlazaRates {
	zero := 0.0
	rates.CacheWrite, rates.CacheWrite1h, rates.CacheWrite12h = &zero, &zero, &zero
	return rates
}

func (p *CustomPlazaRates) fields() []**float64 {
	return []**float64{&p.Input, &p.Output, &p.CacheRead, &p.CacheWrite, &p.CacheWrite1h, &p.CacheWrite12h}
}

func overlayPlazaRates(base, override CustomPlazaRates) CustomPlazaRates {
	fields := base.fields()
	for i, field := range override.fields() {
		if *field != nil {
			*fields[i] = *field
		}
	}
	return base
}
