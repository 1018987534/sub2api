package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const plazaCatalogPrefix = "custom_model_plaza_item:"

var (
	errCustomPlazaModelNotFound = infraerrors.NotFound("CUSTOM_PLAZA_MODEL_NOT_FOUND", "model not found in the model plaza")
	errCustomPlazaDuplicate     = infraerrors.Conflict("CUSTOM_PLAZA_DUPLICATE_MODEL", "this model ID is already in the model plaza")
	errCustomPlazaConflict      = infraerrors.Conflict("CUSTOM_PLAZA_CONFLICT", "the model was changed; reload and try again")
	plazaModelID                = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:+@-]*(/[a-zA-Z0-9][a-zA-Z0-9._:+@-]*)*$`)
)

type CustomPlazaSettingsRepository interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
	GetValuesByPrefixes(context.Context, ...string) (map[string]string, error)
	CompareAndSetValue(ctx context.Context, key string, before, after *string) (bool, error)
}

type CustomPlazaConfig struct {
	ID            string            `json:"id"`
	Family        string            `json:"family"`
	Hidden        bool              `json:"hidden"`
	ContextLimits []int             `json:"context_limits"`
	Tiers         []CustomPlazaTier `json:"tiers"`
}

type CustomPlazaPricePatch struct {
	ID        string              `json:"id"`
	MinTokens int                 `json:"min_tokens"`
	MaxTokens int                 `json:"max_tokens"`
	Rates     map[string]*float64 `json:"rates"` // omitted: keep; null: follow default; 0: explicit zero
}

type plazaRecord struct {
	CustomPlazaConfig
	Deleted bool `json:"deleted,omitempty"`
}

func plazaRecordKey(id string) string {
	// The settings key is limited to 100 bytes; model IDs can be longer and
	// contain namespace separators. Case-insensitive IDs still share a key.
	return fmt.Sprintf("%s%x", plazaCatalogPrefix, sha256.Sum256([]byte(strings.ToLower(id))))
}

func plazaSeed(id string) (customPlazaCatalogEntry, string, bool) {
	for _, section := range customPlazaSeeds {
		for _, model := range section.models {
			if strings.EqualFold(model.id, id) {
				return model, section.family, true
			}
		}
	}
	return customPlazaCatalogEntry{id: id, priceModel: id}, "", false
}

func validatePlazaIdentity(id, family string) error {
	if len(id) > 200 || !plazaModelID.MatchString(id) {
		return infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_ID", "enter a valid model ID (1–200 characters)")
	}
	if family == "" || utf8.RuneCountInString(family) > 40 || strings.ContainsFunc(family, unicode.IsControl) {
		return infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_FAMILY", "enter a model family (1–40 characters)")
	}
	return nil
}

func validatePlazaLimits(limits []int) error {
	previous := 0
	if len(limits) <= 7 {
		for _, limit := range limits {
			if limit <= previous || limit > 1_000_000_000 {
				return infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_LIMITS", "context limits must be increasing positive integers")
			}
			previous = limit
		}
		return nil
	}
	return infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_LIMITS", "use at most seven context limits")
}

func (s *CustomModelPlazaService) quote(ctx context.Context, config CustomPlazaConfig) *CustomPlazaAdminModel {
	entry, seedFamily, seeded := plazaSeed(config.ID)
	name := strings.ToLower(lastSegment(config.ID))
	gpt := seedFamily == "gpt" || strings.EqualFold(config.Family, "gpt") || strings.HasPrefix(name, "gpt-") || strings.HasPrefix(name, "codex-")
	official := s.defaultTiers(ctx, gpt, seeded, entry)
	tiers := official
	if config.ContextLimits != nil {
		tiers = make([]CustomPlazaTier, 0, len(config.ContextLimits)+1)
		min, source := 0, 0
		for _, max := range append(append([]int{}, config.ContextLimits...), 0) {
			// A missing price inherits the official rate at the start of this tier.
			for source+1 < len(official) && official[source+1].MinTokens <= min {
				source++
			}
			tiers = append(tiers, CustomPlazaTier{MinTokens: min, MaxTokens: max, CustomPlazaRates: official[source].CustomPlazaRates})
			min = max
		}
	}
	overrides := config.Tiers
	if overrides == nil {
		overrides = []CustomPlazaTier{}
	}
	return &CustomPlazaAdminModel{
		ID: config.ID, Family: config.Family, Hidden: config.Hidden, GPTCacheFree: gpt,
		ContextLimits: config.ContextLimits, OfficialTiers: official, DefaultTiers: tiers, Overrides: overrides,
	}
}

func (s *CustomModelPlazaService) PreviewDefaults(ctx context.Context, id, family string) (*CustomPlazaAdminModel, error) {
	id, family = strings.TrimSpace(id), strings.TrimSpace(family)
	if err := validatePlazaIdentity(id, family); err != nil {
		return nil, err
	}
	return s.quote(ctx, CustomPlazaConfig{ID: id, Family: family}), nil
}

func readPlazaRecord(values map[string]string, entry customPlazaCatalogEntry, family string) (plazaRecord, *string, error) {
	if raw, ok := values[plazaRecordKey(entry.id)]; ok {
		var record plazaRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return record, nil, fmt.Errorf("decode plaza model %s: %w", entry.id, err)
		}
		return record, &raw, nil
	}
	record := plazaRecord{CustomPlazaConfig: CustomPlazaConfig{ID: entry.id, Family: family}}
	if raw := values[customPlazaSettingPrefix+entry.id]; family != "" && raw != "" {
		if err := json.Unmarshal([]byte(raw), &record.Tiers); err != nil {
			return record, nil, fmt.Errorf("decode legacy plaza price for %s: %w", entry.id, err)
		}
	}
	return record, nil, nil
}

func (s *CustomModelPlazaService) ListAdmin(ctx context.Context) ([]CustomPlazaAdminModel, error) {
	if s.settings == nil {
		return nil, fmt.Errorf("model plaza storage not configured")
	}
	values, err := s.settings.GetValuesByPrefixes(ctx, plazaCatalogPrefix, customPlazaSettingPrefix)
	if err != nil {
		return nil, fmt.Errorf("read plaza catalog: %w", err)
	}
	models := make([]CustomPlazaAdminModel, 0)
	for _, section := range customPlazaSeeds {
		for _, seed := range section.models {
			record, _, err := readPlazaRecord(values, seed, section.family)
			if err != nil {
				return nil, err
			}
			delete(values, plazaRecordKey(seed.id))
			if !record.Deleted {
				models = append(models, *s.quote(ctx, record.CustomPlazaConfig))
			}
		}
	}
	var extra []CustomPlazaAdminModel
	for key, raw := range values {
		if !strings.HasPrefix(key, plazaCatalogPrefix) {
			continue
		}
		var record plazaRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode added plaza model: %w", err)
		}
		if !record.Deleted {
			extra = append(extra, *s.quote(ctx, record.CustomPlazaConfig))
		}
	}
	sort.Slice(extra, func(i, j int) bool { return strings.ToLower(extra[i].ID) < strings.ToLower(extra[j].ID) })
	return append(models, extra...), nil
}

func (s *CustomModelPlazaService) lookup(ctx context.Context, id string) (plazaRecord, *string, bool, error) {
	if s.settings == nil {
		return plazaRecord{}, nil, false, fmt.Errorf("model plaza storage not configured")
	}
	entry, family, seed := plazaSeed(id)
	values, err := s.settings.GetMultiple(ctx, []string{plazaRecordKey(id), customPlazaSettingPrefix + entry.id})
	if err != nil {
		return plazaRecord{}, nil, seed, err
	}
	record, raw, err := readPlazaRecord(values, entry, family)
	return record, raw, seed, err
}

func (s *CustomModelPlazaService) GetAdminModel(ctx context.Context, id string) (*CustomPlazaAdminModel, error) {
	record, raw, seeded, err := s.lookup(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if record.Deleted || (!seeded && raw == nil) {
		return nil, errCustomPlazaModelNotFound
	}
	return s.quote(ctx, record.CustomPlazaConfig), nil
}

func (s *CustomModelPlazaService) Configure(ctx context.Context, config CustomPlazaConfig, create bool) (*CustomPlazaAdminModel, error) {
	config.ID, config.Family = strings.TrimSpace(config.ID), strings.TrimSpace(config.Family)
	if err := validatePlazaIdentity(config.ID, config.Family); err != nil {
		return nil, err
	}
	current, raw, seeded, err := s.lookup(ctx, config.ID)
	if err != nil {
		return nil, err
	}
	exists := !current.Deleted && (seeded || raw != nil)
	if create && exists {
		return nil, errCustomPlazaDuplicate
	}
	if !create && !exists {
		return nil, errCustomPlazaModelNotFound
	}
	if !create {
		config.ID = current.ID
	}
	return s.savePlazaConfig(ctx, config, raw)
}

func (s *CustomModelPlazaService) SetOverrides(ctx context.Context, id string, overrides []CustomPlazaTier) (*CustomPlazaAdminModel, error) {
	record, raw, seeded, err := s.lookup(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if record.Deleted || (!seeded && raw == nil) {
		return nil, errCustomPlazaModelNotFound
	}
	record.Tiers = overrides
	return s.savePlazaConfig(ctx, record.CustomPlazaConfig, raw)
}

func (s *CustomModelPlazaService) PatchPrices(ctx context.Context, patch CustomPlazaPricePatch) (*CustomPlazaAdminModel, error) {
	record, raw, seeded, err := s.lookup(ctx, strings.TrimSpace(patch.ID))
	if err != nil {
		return nil, err
	}
	if record.Deleted || (!seeded && raw == nil) {
		return nil, errCustomPlazaModelNotFound
	}
	if len(patch.Rates) == 0 {
		return nil, infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_PRICE", "provide at least one display price field")
	}
	tier := CustomPlazaTier{MinTokens: patch.MinTokens, MaxTokens: patch.MaxTokens}
	index := len(record.Tiers)
	for i, old := range record.Tiers {
		if old.bounds() == tier.bounds() {
			tier, index = old, i
			break
		}
	}
	fields := map[string]**float64{"input": &tier.Input, "output": &tier.Output, "cache_read": &tier.CacheRead,
		"cache_write": &tier.CacheWrite, "cache_write_1h": &tier.CacheWrite1h, "cache_write_12h": &tier.CacheWrite12h}
	for key, value := range patch.Rates {
		field := fields[key]
		if field == nil {
			return nil, infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_PRICE", "unknown display price field")
		}
		*field = value
	}
	if index == len(record.Tiers) {
		record.Tiers = append(record.Tiers, tier)
	} else {
		record.Tiers[index] = tier
	}
	return s.savePlazaConfig(ctx, record.CustomPlazaConfig, raw)
}

func (s *CustomModelPlazaService) DeleteModel(ctx context.Context, id string) error {
	record, raw, seeded, err := s.lookup(ctx, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if record.Deleted || (!seeded && raw == nil) {
		return errCustomPlazaModelNotFound
	}
	if seeded {
		// Prevent a deleted starter model from reappearing at the next read.
		return s.writePlazaRecord(ctx, record.ID, raw, &plazaRecord{CustomPlazaConfig: CustomPlazaConfig{ID: record.ID}, Deleted: true})
	}
	return s.writePlazaRecord(ctx, record.ID, raw, nil)
}

func (s *CustomModelPlazaService) savePlazaConfig(ctx context.Context, config CustomPlazaConfig, before *string) (*CustomPlazaAdminModel, error) {
	if err := validatePlazaLimits(config.ContextLimits); err != nil {
		return nil, err
	}
	result := s.quote(ctx, config)
	known, seen := map[[2]int]bool{}, map[[2]int]bool{}
	for _, tier := range result.DefaultTiers {
		known[tier.bounds()] = true
	}
	result.Overrides = []CustomPlazaTier{}
	for _, tier := range config.Tiers {
		bounds := tier.bounds()
		if !known[bounds] || seen[bounds] {
			return nil, infraerrors.BadRequest("CUSTOM_PLAZA_INVALID_TIER", "unknown or duplicate context tier")
		}
		seen[bounds] = true
		if result.GPTCacheFree && (tier.CacheWrite != nil || tier.CacheWrite1h != nil || tier.CacheWrite12h != nil) {
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
	config.Tiers = result.Overrides
	if err := s.writePlazaRecord(ctx, config.ID, before, &plazaRecord{CustomPlazaConfig: config}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CustomModelPlazaService) writePlazaRecord(ctx context.Context, id string, before *string, after *plazaRecord) error {
	var value *string
	if after != nil {
		encoded, err := json.Marshal(after)
		if err != nil {
			return err
		}
		raw := string(encoded)
		value = &raw
	}
	ok, err := s.settings.CompareAndSetValue(ctx, plazaRecordKey(id), before, value)
	if err != nil {
		return fmt.Errorf("write plaza model: %w", err)
	}
	if !ok {
		return errCustomPlazaConflict
	}
	return nil
}
