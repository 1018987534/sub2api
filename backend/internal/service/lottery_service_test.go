package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateLotteryConfigDefaultsAndActorBoundary(t *testing.T) {
	cfg := LotteryConfig{
		ParticipantThreshold: 50,
		PrizeCount:           6,
		PrizeAmount:          5,
		DrawMode:             LotteryDrawModeAuto,
		NextRoundMode:        LotteryRoundModeManual,
		ActorPercentage:      50,
		ActorJoinMinSeconds:  60,
		ActorJoinMaxSeconds:  600,
		RequireRecharge:      true,
	}
	require.NoError(t, ValidateLotteryConfig(cfg))
	require.Equal(t, 25, lotteryActorTarget(cfg.ParticipantThreshold, cfg.ActorPercentage))
	require.Equal(t, 25, lotteryRealSlotCapacity(cfg.ParticipantThreshold, lotteryActorTarget(cfg.ParticipantThreshold, cfg.ActorPercentage)))

	cfg.PrizeCount = 26
	err := ValidateLotteryConfig(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "real participant slots")
}

func TestValidateLotteryConfigRejectsInvalidModesAndIntervals(t *testing.T) {
	cfg := LotteryConfig{ParticipantThreshold: 50, PrizeCount: 1, PrizeAmount: 5, DrawMode: "later", NextRoundMode: LotteryRoundModeManual, ActorJoinMinSeconds: 60, ActorJoinMaxSeconds: 600}
	require.Error(t, ValidateLotteryConfig(cfg))
	cfg.DrawMode = LotteryDrawModeAuto
	cfg.ActorJoinMinSeconds = 700
	require.Error(t, ValidateLotteryConfig(cfg))
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
