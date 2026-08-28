package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateLotteryConfigDefaultsAndPrizeBoundary(t *testing.T) {
	cfg := LotteryConfig{
		ParticipantThreshold: 50,
		PrizeCount:           6,
		PrizeAmount:          5,
		DrawMode:             LotteryDrawModeAuto,
		NextRoundMode:        LotteryRoundModeManual,
		RequireRecharge:      true,
	}
	require.NoError(t, ValidateLotteryConfig(cfg))

	cfg.PrizeCount = 51
	err := ValidateLotteryConfig(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "participant threshold")
}

func TestValidateLotteryConfigRejectsInvalidModes(t *testing.T) {
	cfg := LotteryConfig{ParticipantThreshold: 50, PrizeCount: 1, PrizeAmount: 5, DrawMode: "later", NextRoundMode: LotteryRoundModeManual}
	require.Error(t, ValidateLotteryConfig(cfg))
}

func TestValidateLotteryProgressBounds(t *testing.T) {
	round := LotteryRound{RealParticipantCount: 12, ParticipantThreshold: 50, PrizeCount: 6}
	require.NoError(t, validateLotteryProgress(round, 12))
	require.NoError(t, validateLotteryProgress(round, 50))
	require.ErrorIs(t, validateLotteryProgress(round, 11), ErrLotteryProgressInvalid)
	require.ErrorIs(t, validateLotteryProgress(round, 51), ErrLotteryProgressInvalid)

	round.RealParticipantCount = 5
	require.NoError(t, validateLotteryProgress(round, 49))
	require.ErrorIs(t, validateLotteryProgress(round, 50), ErrLotteryInsufficientRealParticipants)
}

func TestMaskLotteryEmail(t *testing.T) {
	require.Equal(t, "mh***o@gmail.com", MaskLotteryEmail("mhdeyao@gmail.com"))
	require.Equal(t, "a***@example.com", MaskLotteryEmail("a@example.com"))
	require.Equal(t, "***", MaskLotteryEmail("invalid"))
}

func TestLotteryPublicPayloadHidesRecentRechargeRule(t *testing.T) {
	round := LotteryRound{
		ID:                   1,
		RoundNo:              10,
		ParticipantThreshold: 50,
		PrizeCount:           6,
		PrizeAmount:          5,
		RecentRechargeDays:   30,
	}
	publicRound := toLotteryPublicRound(round)
	current := LotteryCurrent{
		Enabled:      true,
		CurrentRound: &publicRound,
		Eligibility: LotteryEligibility{
			Eligible:               false,
			Reason:                 "not_eligible",
			HiddenRecentRuleFailed: true,
		},
	}

	payload, err := json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "recent_recharge")
	require.NotContains(t, string(payload), "hidden_recent")
	require.Contains(t, string(payload), `"reason":"not_eligible"`)
}

func TestLotteryPublicRoundIncludesDrawMode(t *testing.T) {
	publicRound := toLotteryPublicRound(LotteryRound{DrawMode: LotteryDrawModeManual})
	require.Equal(t, LotteryDrawModeManual, publicRound.DrawMode)
}
