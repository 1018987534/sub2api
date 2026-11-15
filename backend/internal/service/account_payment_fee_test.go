package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountPaymentFeeCostsAndScheduling(t *testing.T) {
	base := 0.1
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: &base, Extra: map[string]any{UpstreamPaymentFeePercentExtraKey: 3.0}}
	for i := 0; i < 3; i++ {
		require.Equal(t, 0.103, a.BillingRateMultiplier())
		rate, ok := openAISchedulingRate(a, time.Now(), nil)
		require.True(t, ok)
		require.Equal(t, 0.103, rate)
		require.Equal(t, 0.1, a.BaseBillingRateMultiplier())
	}
	require.InDelta(t, 10.3, 100*a.BillingRateMultiplier(), 1e-12)
	a.RateMultiplier = nil
	require.Equal(t, 1.03, a.BillingRateMultiplier())
	zero := 0.0
	a.RateMultiplier = &zero
	require.Zero(t, a.BillingRateMultiplier())
	a.RateMultiplier = &base
	delete(a.Extra, UpstreamPaymentFeePercentExtraKey)
	require.Equal(t, 0.1, a.BillingRateMultiplier())
}

func TestAccountPaymentFeeProfitAdmission(t *testing.T) {
	base := 0.1
	a := &Account{RateMultiplier: &base, Extra: map[string]any{UpstreamPaymentFeePercentExtraKey: 3.0}}
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{threshold: 0.102})
	vetoed, reason := openAIProfitControlVetoReason(ctx, a)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	preview := previewAccountProfitAdmission(a, true, 0.102, 0.102, time.Now())
	require.Equal(t, ProfitPreviewClassRejectedThreshold, preview.Class)
	require.Equal(t, 0.103, *preview.AccountRate)
	a.Extra[UpstreamPaymentFeePercentExtraKey] = 0.0
	vetoed, _ = openAIProfitControlVetoReason(ctx, a)
	require.False(t, vetoed)
}

func TestAccountPaymentFeeWithSyncedProbeAndPeak(t *testing.T) {
	data := map[string]any{
		"billing_scope": "token", "group_rate_multiplier": 2.0,
		"resolved_rate_multiplier": 2.0, "effective_rate_multiplier": 4.0,
		"peak_rate_enabled": true, "applied_peak_multiplier": 2.0,
		"peak_rate_multiplier": 2.0, "peak_start": "10:00", "peak_end": "12:00", "timezone": "UTC",
	}
	now := time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		normalized, base, ok := NormalizeUpstreamBillingProbeData(data, 0.05)
		require.True(t, ok)
		a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: &base, Extra: map[string]any{
			UpstreamPaymentFeePercentExtraKey: 3.0,
		}}
		// Probe snapshots are JSON maps in production.
		a.Extra[UpstreamBillingProbeExtraKey] = map[string]any{
			"status": "ok", "received_at": now.Format(time.RFC3339), "fresh_until": now.Add(time.Hour).Format(time.RFC3339), "data": normalized,
		}
		require.Equal(t, 0.103, a.BillingRateMultiplier())
		rate, ok := openAISchedulingRate(a, now, nil)
		require.True(t, ok)
		require.Equal(t, 0.206, rate)
		require.Equal(t, 2.0, normalized["declared_resolved_rate_multiplier"])
		data = normalized
	}
}

func TestAccountPaymentFeeValidation(t *testing.T) {
	for _, value := range []any{-1.0, 100.01, math.NaN(), math.Inf(1), true, nil, "wrong", "3"} {
		require.Error(t, ValidateUpstreamPaymentFeeExtra(map[string]any{UpstreamPaymentFeePercentExtraKey: value}))
	}
	for _, value := range []float64{0, 0.01, 3, 100} {
		require.NoError(t, ValidateUpstreamPaymentFeeExtra(map[string]any{UpstreamPaymentFeePercentExtraKey: value}))
	}
}

func TestAccountPaymentFeeSavePreserveClear(t *testing.T) {
	base := 0.1
	created, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: &base}, map[string]any{UpstreamPaymentFeePercentExtraKey: 3.0})
	require.NoError(t, err)
	require.Equal(t, 0.103, created.BillingRateMultiplier())
	created.ID = 1
	repo := &accountBillingSettingsAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: created}}}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{"unrelated": true}})
	require.NoError(t, err)
	require.Equal(t, 0.103, updated.BillingRateMultiplier())
	updated, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{UpstreamPaymentFeePercentExtraKey: 0.0}})
	require.NoError(t, err)
	require.Equal(t, 0.1, updated.BillingRateMultiplier())
	_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{UpstreamPaymentFeePercentExtraKey: -1.0}})
	require.Error(t, err)
	require.Equal(t, 0.1, repo.accounts[1].BillingRateMultiplier())
}
