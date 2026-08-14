package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	upstreamBillingPriceEvidenceWindow = 24 * time.Hour
	upstreamBillingPriceEvidenceLimit  = 500
	// $10,000 per million tokens is intentionally generous while still rejecting
	// per-request/image costs that accidentally reach this token-only path.
	upstreamBillingPriceEvidenceMaxPerToken = 0.01
)

// UpstreamBillingInferredModelPrice is a sanitized summary derived from recent
// usage rows for one API key. It contains no request, user, IP, or key metadata.
// The component sample counters make partial candidates explicit to operators.
type UpstreamBillingInferredModelPrice struct {
	UpstreamBillingModelPrice
	SampleCount              int       `json:"sample_count"`
	InputSampleCount         int       `json:"input_sample_count,omitempty"`
	OutputSampleCount        int       `json:"output_sample_count,omitempty"`
	CacheCreationSampleCount int       `json:"cache_creation_sample_count,omitempty"`
	CacheReadSampleCount     int       `json:"cache_read_sample_count,omitempty"`
	ObservedAt               time.Time `json:"observed_at"`
}

type upstreamBillingRecentPriceEvidenceReader interface {
	ListRecentTokenPricingEvidence(context.Context, int64, time.Time, int) ([]UsageLog, error)
}

var ErrUpstreamBillingPriceCandidateNotFound = infraerrors.NotFound(
	"UPSTREAM_BILLING_PRICE_CANDIDATE_NOT_FOUND",
	"upstream billing price candidate not found",
)

// UpstreamBillingPriceDiscrepancy is the administrator-only comparison shown
// in the pricing workspace. Current and inferred prices are USD per token.
type UpstreamBillingPriceDiscrepancy struct {
	AccountID     int64                             `json:"account_id"`
	AccountName   string                            `json:"account_name"`
	Model         string                            `json:"model"`
	CurrentSource string                            `json:"current_source"`
	CurrentPrice  UpstreamBillingModelPrice         `json:"current_price"`
	InferredPrice UpstreamBillingInferredModelPrice `json:"inferred_price"`
}

// InferAPIKeyUpstreamBillingPrices reads a bounded recent window outside the
// customer request path. Component costs are divided by their matching token
// counts; no unstable multi-variable fit against total_cost is needed.
func (s *UsageService) InferAPIKeyUpstreamBillingPrices(
	ctx context.Context,
	apiKeyID int64,
	now time.Time,
) (map[string]UpstreamBillingInferredModelPrice, error) {
	if s == nil || s.usageRepo == nil || apiKeyID <= 0 {
		return nil, nil
	}
	reader, ok := s.usageRepo.(upstreamBillingRecentPriceEvidenceReader)
	if !ok {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	logs, err := reader.ListRecentTokenPricingEvidence(
		ctx,
		apiKeyID,
		now.UTC().Add(-upstreamBillingPriceEvidenceWindow),
		upstreamBillingPriceEvidenceLimit,
	)
	if err != nil {
		return nil, err
	}
	return inferUpstreamBillingPricesFromUsageLogs(logs), nil
}

func inferUpstreamBillingPricesFromUsageLogs(logs []UsageLog) map[string]UpstreamBillingInferredModelPrice {
	result := make(map[string]UpstreamBillingInferredModelPrice)
	for i := range logs {
		row := &logs[i]
		if row == nil || row.LongContextBillingApplied || !usageRowSupportsPriceInference(row) {
			continue
		}
		model := strings.ToLower(strings.TrimSpace(row.Model))
		if model == "" || len([]rune(model)) > upstreamResponseModelMaxLength {
			continue
		}

		candidate := result[model]
		used := false
		if price, ok := inferTokenComponentPrice(row.InputCost, row.InputTokens); ok {
			candidate.InputPricePerToken = math.Max(candidate.InputPricePerToken, price)
			candidate.InputSampleCount++
			used = true
		}
		if price, ok := inferTokenComponentPrice(row.OutputCost, row.OutputTokens); ok {
			candidate.OutputPricePerToken = math.Max(candidate.OutputPricePerToken, price)
			candidate.OutputSampleCount++
			used = true
		}
		if price, ok := inferCacheCreationPrice(row); ok {
			candidate.CacheCreationPricePerToken = math.Max(candidate.CacheCreationPricePerToken, price)
			if row.CacheCreation5mTokens > 0 && row.CacheCreation1hTokens == 0 {
				candidate.CacheCreation5mPrice = math.Max(candidate.CacheCreation5mPrice, price)
			}
			if row.CacheCreation1hTokens > 0 && row.CacheCreation5mTokens == 0 {
				candidate.CacheCreation1hPrice = math.Max(candidate.CacheCreation1hPrice, price)
			}
			candidate.CacheCreationSampleCount++
			used = true
		}
		if price, ok := inferTokenComponentPrice(row.CacheReadCost, row.CacheReadTokens); ok {
			candidate.CacheReadPricePerToken = math.Max(candidate.CacheReadPricePerToken, price)
			candidate.CacheReadSampleCount++
			used = true
		}
		if !used {
			continue
		}
		candidate.SampleCount++
		if row.CreatedAt.After(candidate.ObservedAt) {
			candidate.ObservedAt = row.CreatedAt.UTC()
		}
		result[model] = candidate
	}

	for model, candidate := range result {
		if candidate.SampleCount <= 0 || candidate.ObservedAt.IsZero() ||
			(candidate.InputPricePerToken <= 0 && candidate.OutputPricePerToken <= 0) {
			delete(result, model)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func usageRowSupportsPriceInference(row *UsageLog) bool {
	if row.BillingMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*row.BillingMode))
		if mode != "" && mode != string(BillingModeToken) {
			return false
		}
	}
	if row.ServiceTier != nil {
		tier := strings.ToLower(strings.TrimSpace(*row.ServiceTier))
		if tier != "" && tier != "default" && tier != "standard" {
			return false
		}
	}
	return true
}

func inferTokenComponentPrice(cost float64, tokens int) (float64, bool) {
	if tokens <= 0 || cost <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, false
	}
	price := cost / float64(tokens)
	if price <= 0 || price > upstreamBillingPriceEvidenceMaxPerToken || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, false
	}
	// Usage cost columns retain ten decimal places. Quantizing the derived value
	// keeps JSON snapshots stable without erasing sub-dollar-per-million prices.
	return math.Round(price*1e15) / 1e15, true
}

func inferCacheCreationPrice(row *UsageLog) (float64, bool) {
	if row == nil || (row.CacheCreation5mTokens > 0 && row.CacheCreation1hTokens > 0) {
		return 0, false
	}
	return inferTokenComponentPrice(row.CacheCreationCost, row.CacheCreationTokens)
}

func normalizeUpstreamBillingInferredModelPrices(
	raw map[string]UpstreamBillingInferredModelPrice,
) (map[string]UpstreamBillingInferredModelPrice, bool) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, false
	}
	result := make(map[string]UpstreamBillingInferredModelPrice, len(raw))
	for model, evidence := range raw {
		model = strings.ToLower(strings.TrimSpace(model))
		if model == "" || len([]rune(model)) > upstreamResponseModelMaxLength ||
			evidence.SampleCount <= 0 || evidence.ObservedAt.IsZero() ||
			evidence.InputSampleCount < 0 || evidence.OutputSampleCount < 0 ||
			evidence.CacheCreationSampleCount < 0 || evidence.CacheReadSampleCount < 0 ||
			!validUpstreamBillingModelPrice(evidence.UpstreamBillingModelPrice) {
			return nil, false
		}
		evidence.ObservedAt = evidence.ObservedAt.UTC()
		result[model] = evidence
	}
	return result, len(result) > 0
}

func parseUpstreamBillingInferredModelPrices(value any) (map[string]UpstreamBillingInferredModelPrice, bool) {
	if value == nil {
		return nil, false
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var prices map[string]UpstreamBillingInferredModelPrice
	if err := json.Unmarshal(body, &prices); err != nil {
		return nil, false
	}
	return normalizeUpstreamBillingInferredModelPrices(prices)
}

// ListPriceDiscrepancies compares inferred usage evidence with the price that
// would currently bill each opted-in account. It is an admin read path and is
// never consulted while serving a customer request.
func (s *UpstreamBillingProbeService) ListPriceDiscrepancies(ctx context.Context) ([]UpstreamBillingPriceDiscrepancy, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	accounts, err := s.accountRepo.FindByExtraField(ctx, UpstreamBillingPriceSyncEnabledExtraKey, true)
	if err != nil {
		return nil, fmt.Errorf("list upstream price sync accounts: %w", err)
	}
	result := make([]UpstreamBillingPriceDiscrepancy, 0)
	for i := range accounts {
		account := &accounts[i]
		for model, inferred := range inferredPricesFromAccount(account) {
			current, source, ok := s.currentConfirmedUpstreamPrice(account, model)
			if !ok || !inferredPriceDiffers(current, inferred) {
				continue
			}
			result = append(result, UpstreamBillingPriceDiscrepancy{
				AccountID: account.ID, AccountName: account.Name, Model: model,
				CurrentSource: source, CurrentPrice: current, InferredPrice: inferred,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InferredPrice.ObservedAt.Equal(result[j].InferredPrice.ObservedAt) {
			if result[i].AccountID == result[j].AccountID {
				return result[i].Model < result[j].Model
			}
			return result[i].AccountID < result[j].AccountID
		}
		return result[i].InferredPrice.ObservedAt.After(result[j].InferredPrice.ObservedAt)
	})
	return result, nil
}

// ConfirmInferredPrice copies only evidenced fields into the account's manual
// price snapshot. The latest candidate is re-read at click time so stale UI
// state cannot apply a model that is no longer present.
func (s *UpstreamBillingProbeService) ConfirmInferredPrice(
	ctx context.Context,
	accountID int64,
	model string,
) (*UpstreamBillingPriceDiscrepancy, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if accountID <= 0 || model == "" {
		return nil, ErrUpstreamBillingPriceCandidateNotFound
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !upstreamBillingPriceSyncEnabled(account) {
		return nil, ErrUpstreamBillingPriceCandidateNotFound
	}
	inferred, ok := inferredPricesFromAccount(account)[model]
	if !ok {
		return nil, ErrUpstreamBillingPriceCandidateNotFound
	}
	current, source, ok := s.currentConfirmedUpstreamPrice(account, model)
	if !ok {
		return nil, ErrUpstreamBillingPriceCandidateNotFound
	}
	confirmed := mergeInferredUpstreamPrice(current, inferred)
	if !validUpstreamBillingModelPrice(confirmed) {
		return nil, ErrUpstreamBillingPriceCandidateNotFound
	}
	manual, _ := parseUpstreamBillingModelPrices(account.Extra[UpstreamBillingManualModelPricesExtraKey])
	if manual == nil {
		manual = make(map[string]UpstreamBillingModelPrice)
	}
	manual[model] = confirmed
	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		UpstreamBillingManualModelPricesExtraKey: manual,
	}); err != nil {
		return nil, fmt.Errorf("confirm upstream billing price: %w", err)
	}
	return &UpstreamBillingPriceDiscrepancy{
		AccountID: account.ID, AccountName: account.Name, Model: model,
		CurrentSource: source, CurrentPrice: current, InferredPrice: inferred,
	}, nil
}

func inferredPricesFromAccount(account *Account) map[string]UpstreamBillingInferredModelPrice {
	snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra)
	if snapshot == nil || snapshot.Data == nil {
		return nil
	}
	prices, _ := parseUpstreamBillingInferredModelPrices(snapshot.Data[upstreamBillingInferredModelPricesDataKey])
	return prices
}

func (s *UpstreamBillingProbeService) currentConfirmedUpstreamPrice(
	account *Account,
	model string,
) (UpstreamBillingModelPrice, string, bool) {
	if account == nil {
		return UpstreamBillingModelPrice{}, "", false
	}
	if manual, valid := parseUpstreamBillingModelPrices(account.Extra[UpstreamBillingManualModelPricesExtraKey]); valid {
		if price, ok := lookupUpstreamBillingModelPrice(manual, model); ok {
			return price, upstreamBillingManualPricingSource, true
		}
	}
	if snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra); snapshot != nil && snapshot.Data != nil {
		if synced, valid := parseUpstreamBillingModelPrices(snapshot.Data[upstreamBillingModelPricesDataKey]); valid {
			if price, ok := lookupUpstreamBillingModelPrice(synced, model); ok {
				return price, upstreamBillingPricingSource, true
			}
		}
	}
	if s.billingService == nil {
		return UpstreamBillingModelPrice{}, "", false
	}
	pricing, err := s.billingService.GetModelPricing(model)
	if err != nil {
		return UpstreamBillingModelPrice{}, "", false
	}
	price, ok := upstreamBillingModelPriceFromModelPricing(pricing)
	return price, "local", ok
}

func inferredPriceDiffers(current UpstreamBillingModelPrice, inferred UpstreamBillingInferredModelPrice) bool {
	checks := []struct {
		samples  int
		current  float64
		inferred float64
	}{
		{inferred.InputSampleCount, current.InputPricePerToken, inferred.InputPricePerToken},
		{inferred.OutputSampleCount, current.OutputPricePerToken, inferred.OutputPricePerToken},
		{inferred.CacheCreationSampleCount, current.CacheCreationPricePerToken, inferred.CacheCreationPricePerToken},
		{inferred.CacheReadSampleCount, current.CacheReadPricePerToken, inferred.CacheReadPricePerToken},
	}
	for _, check := range checks {
		if check.samples > 0 && !sameInferredPrice(check.current, check.inferred) {
			return true
		}
	}
	return false
}

func sameInferredPrice(left, right float64) bool {
	delta := math.Abs(left - right)
	scale := math.Max(math.Abs(left), math.Abs(right))
	return delta <= math.Max(1e-15, scale*1e-6)
}

func mergeInferredUpstreamPrice(
	current UpstreamBillingModelPrice,
	inferred UpstreamBillingInferredModelPrice,
) UpstreamBillingModelPrice {
	if inferred.InputSampleCount > 0 {
		current.InputPricePerToken = inferred.InputPricePerToken
	}
	if inferred.OutputSampleCount > 0 {
		current.OutputPricePerToken = inferred.OutputPricePerToken
	}
	if inferred.CacheCreationSampleCount > 0 {
		current.CacheCreationPricePerToken = inferred.CacheCreationPricePerToken
		if inferred.CacheCreation5mPrice > 0 {
			current.CacheCreation5mPrice = inferred.CacheCreation5mPrice
		}
		if inferred.CacheCreation1hPrice > 0 {
			current.CacheCreation1hPrice = inferred.CacheCreation1hPrice
		}
	}
	if inferred.CacheReadSampleCount > 0 {
		current.CacheReadPricePerToken = inferred.CacheReadPricePerToken
	}
	return current
}
