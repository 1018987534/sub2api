//go:build unit

package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAdminBindInviter(t *testing.T) {
	t.Parallel()

	t.Run("binds an unbound customer with admin source", func(t *testing.T) {
		t.Parallel()
		repo := &paymentFulfillmentAffiliateRepoStub{
			inviteeSummary: &AffiliateSummary{UserID: 21},
			inviterSummary: &AffiliateSummary{UserID: 10},
			bindResult:     true,
		}
		svc := &AffiliateService{repo: repo}

		require.NoError(t, svc.AdminBindInviter(context.Background(), 21, 10))
		require.Len(t, repo.bindCalls, 1)
		require.Equal(t, int64(21), repo.bindCalls[0].inviteeID)
		require.Equal(t, int64(10), repo.bindCalls[0].inviterID)
		require.Equal(t, AffiliateBindingSourceAdmin, repo.bindCalls[0].source)
	})

	t.Run("rejects self binding", func(t *testing.T) {
		t.Parallel()
		svc := &AffiliateService{repo: &paymentFulfillmentAffiliateRepoStub{}}
		err := svc.AdminBindInviter(context.Background(), 10, 10)
		require.ErrorIs(t, err, ErrAffiliateSelfBinding)
	})

	t.Run("does not overwrite an existing relationship", func(t *testing.T) {
		t.Parallel()
		oldInviterID := int64(9)
		repo := &paymentFulfillmentAffiliateRepoStub{
			inviteeSummary: &AffiliateSummary{UserID: 21, InviterID: &oldInviterID},
			inviterSummary: &AffiliateSummary{UserID: 10},
		}
		svc := &AffiliateService{repo: repo}

		err := svc.AdminBindInviter(context.Background(), 21, 10)
		require.ErrorIs(t, err, ErrAffiliateAlreadyBound)
		require.Empty(t, repo.bindCalls)
	})

	t.Run("is idempotent for the same admin match", func(t *testing.T) {
		t.Parallel()
		inviterID := int64(10)
		repo := &paymentFulfillmentAffiliateRepoStub{
			inviteeSummary: &AffiliateSummary{
				UserID:            21,
				InviterID:         &inviterID,
				InviterBindSource: string(AffiliateBindingSourceAdmin),
			},
		}
		svc := &AffiliateService{repo: repo}

		require.NoError(t, svc.AdminBindInviter(context.Background(), 21, 10))
		require.Empty(t, repo.bindCalls)
	})

	t.Run("reports a concurrent bind as conflict", func(t *testing.T) {
		t.Parallel()
		repo := &paymentFulfillmentAffiliateRepoStub{
			inviteeSummary: &AffiliateSummary{UserID: 21},
			inviterSummary: &AffiliateSummary{UserID: 10},
			bindResult:     false,
		}
		svc := &AffiliateService{repo: repo}

		err := svc.AdminBindInviter(context.Background(), 21, 10)
		require.ErrorIs(t, err, ErrAffiliateAlreadyBound)
	})

	t.Run("propagates repository failures", func(t *testing.T) {
		t.Parallel()
		bindErr := errors.New("database unavailable")
		repo := &paymentFulfillmentAffiliateRepoStub{
			inviteeSummary: &AffiliateSummary{UserID: 21},
			inviterSummary: &AffiliateSummary{UserID: 10},
			bindResult:     true,
			bindErr:        bindErr,
		}
		svc := &AffiliateService{repo: repo}

		require.ErrorIs(t, svc.AdminBindInviter(context.Background(), 21, 10), bindErr)
	})
}

func TestAffiliateRebateStartsAt(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	boundAt := time.Date(2026, 7, 16, 9, 30, 0, 0, time.UTC)

	require.Equal(t, boundAt, affiliateRebateStartsAt(&AffiliateSummary{
		CreatedAt:      createdAt,
		InviterBoundAt: &boundAt,
	}))
	require.Equal(t, createdAt, affiliateRebateStartsAt(&AffiliateSummary{CreatedAt: createdAt}))
}

func TestTransferAffiliateQuotaRequiresDistinctPaidInvitees(t *testing.T) {
	t.Parallel()

	t.Run("blocks below the default five-account requirement", func(t *testing.T) {
		t.Parallel()
		repo := &paymentFulfillmentAffiliateRepoStub{paidInviteeCount: 4}
		svc := &AffiliateService{repo: repo}

		_, _, err := svc.TransferAffiliateQuota(context.Background(), 10)
		require.ErrorIs(t, err, ErrAffiliatePaidInviteesLow)
		require.False(t, repo.transferCalled)
		require.Equal(t, map[string]string{"current": "4", "required": "5"}, infraerrors.FromError(err).Metadata)
	})

	t.Run("passes the configured requirement into the transactional transfer", func(t *testing.T) {
		t.Parallel()
		repo := &paymentFulfillmentAffiliateRepoStub{
			paidInviteeCount: 7,
			transferAmount:   12.5,
			transferBalance:  30,
		}
		settingService := NewSettingService(&paymentFulfillmentSettingRepoStub{values: map[string]string{
			SettingKeyAffiliateMinPaidInvitees: "7",
		}}, nil)
		svc := &AffiliateService{repo: repo, settingService: settingService}

		transferred, balance, err := svc.TransferAffiliateQuota(context.Background(), 10)
		require.NoError(t, err)
		require.Equal(t, 12.5, transferred)
		require.Equal(t, 30.0, balance)
		require.True(t, repo.transferCalled)
		require.Equal(t, 7, repo.transferMinPaidInvitees)
		require.WithinDuration(t, time.Now().Add(-affiliateInviterRecentPaymentWindow), repo.transferRecentPaymentSince, time.Second)
	})
}

func TestGetAffiliateDetailIncludesPaidInviteeTransferProgress(t *testing.T) {
	t.Parallel()
	repo := &paymentFulfillmentAffiliateRepoStub{
		inviterSummary:   &AffiliateSummary{UserID: 10, AffCode: "AFFTEST"},
		paidInviteeCount: 3,
	}
	settingService := NewSettingService(&paymentFulfillmentSettingRepoStub{values: map[string]string{
		SettingKeyAffiliateMinPaidInvitees: "5",
	}}, nil)
	svc := &AffiliateService{repo: repo, settingService: settingService}

	detail, err := svc.GetAffiliateDetail(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 3, detail.PaidInviteeCount)
	require.Equal(t, 5, detail.MinPaidInviteesForTransfer)
}

func TestGetAffiliateMinPaidInvitees(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{name: "configured", value: "7", want: 7},
		{name: "disabled", value: "0", want: 0},
		{name: "negative falls back", value: "-1", want: AffiliateMinPaidInviteesDefault},
		{name: "invalid falls back", value: "invalid", want: AffiliateMinPaidInviteesDefault},
		{name: "clamped", value: "10001", want: AffiliateMinPaidInviteesMax},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := NewSettingService(&paymentFulfillmentSettingRepoStub{values: map[string]string{
				SettingKeyAffiliateMinPaidInvitees: tc.value,
			}}, nil)
			require.Equal(t, tc.want, svc.GetAffiliateMinPaidInvitees(context.Background()))
		})
	}
}

// TestResolveRebateRatePercent_PerUserOverride verifies that per-inviter
// AffRebateRatePercent overrides the global rate, that NULL falls back to the
// global rate, and that out-of-range exclusive rates are clamped silently.
//
// SettingService is left nil here so globalRebateRatePercent returns the
// documented default (AffiliateRebateRateDefault = 20%) — this exercises the
// fallback path without spinning up a settings stub.
func TestResolveRebateRatePercent_PerUserOverride(t *testing.T) {
	t.Parallel()
	svc := &AffiliateService{}

	// nil exclusive rate → falls back to global default (20%)
	require.InDelta(t, AffiliateRebateRateDefault,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{}), 1e-9)

	// exclusive rate set → overrides global
	rate := 50.0
	require.InDelta(t, 50.0,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &rate}), 1e-9)

	// exclusive rate 0 → returns 0 (no rebate, intentional)
	zero := 0.0
	require.InDelta(t, 0.0,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &zero}), 1e-9)

	// exclusive rate above max → clamped to Max
	tooHigh := 250.0
	require.InDelta(t, AffiliateRebateRateMax,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &tooHigh}), 1e-9)

	// exclusive rate below min → clamped to Min
	tooLow := -5.0
	require.InDelta(t, AffiliateRebateRateMin,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &tooLow}), 1e-9)
}

// TestIsEnabled_NilSettingServiceReturnsDefault verifies that IsEnabled
// safely handles a nil settingService dependency by returning the default
// (off). This protects callers from nil-pointer crashes in misconfigured
// environments.
func TestIsEnabled_NilSettingServiceReturnsDefault(t *testing.T) {
	t.Parallel()
	svc := &AffiliateService{}
	require.False(t, svc.IsEnabled(context.Background()))
	require.Equal(t, AffiliateEnabledDefault, svc.IsEnabled(context.Background()))
}

// TestValidateExclusiveRate_BoundaryAndInvalid covers the validator used by
// admin-facing rate setters: nil is always valid (clear), in-range values
// are accepted, NaN/Inf and out-of-range values produce a typed BadRequest.
func TestValidateExclusiveRate_BoundaryAndInvalid(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateExclusiveRate(nil))

	for _, v := range []float64{0, 0.01, 50, 99.99, 100} {
		v := v
		require.NoError(t, validateExclusiveRate(&v), "value %v should be valid", v)
	}

	for _, v := range []float64{-0.01, 100.01, -100, 200} {
		v := v
		require.Error(t, validateExclusiveRate(&v), "value %v should be rejected", v)
	}

	nan := math.NaN()
	require.Error(t, validateExclusiveRate(&nan))
	posInf := math.Inf(1)
	require.Error(t, validateExclusiveRate(&posInf))
	negInf := math.Inf(-1)
	require.Error(t, validateExclusiveRate(&negInf))
}

func TestMaskEmail(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a***@g***.com", maskEmail("alice@gmail.com"))
	require.Equal(t, "x***@d***", maskEmail("x@domain"))
	require.Equal(t, "", maskEmail(""))
}

func TestIsValidAffiliateCodeFormat(t *testing.T) {
	t.Parallel()

	// 邀请码格式校验同时服务于：
	// 1) 系统自动生成的 12 位随机码（A-Z 去 I/O，2-9 去 0/1）
	// 2) 管理员设置的自定义专属码（如 "VIP2026"、"NEW_USER-1"）
	// 因此校验放宽到 [A-Z0-9_-]{4,32}（要求调用方先 ToUpper）。
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid canonical 12-char", "ABCDEFGHJKLM", true},
		{"valid all digits 2-9", "234567892345", true},
		{"valid mixed", "A2B3C4D5E6F7", true},
		{"valid admin custom short", "VIP1", true},
		{"valid admin custom with hyphen", "NEW-USER", true},
		{"valid admin custom with underscore", "VIP_2026", true},
		{"valid 32-char max", "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", true},
		// Previously-excluded chars (I/O/0/1) are now allowed since admins may use them.
		{"letter I now allowed", "IBCDEFGHJKLM", true},
		{"letter O now allowed", "OBCDEFGHJKLM", true},
		{"digit 0 now allowed", "0BCDEFGHJKLM", true},
		{"digit 1 now allowed", "1BCDEFGHJKLM", true},
		{"too short (3 chars)", "ABC", false},
		{"too long (33 chars)", "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456", false},
		{"lowercase rejected (caller must ToUpper first)", "abcdefghjklm", false},
		{"empty", "", false},
		{"utf8 non-ascii", "ÄÄÄÄÄÄ", false}, // bytes out of charset
		{"ascii punctuation .", "ABCDEFGHJK.M", false},
		{"whitespace", "ABCDEFGHJK M", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isValidAffiliateCodeFormat(tc.in))
		})
	}
}
