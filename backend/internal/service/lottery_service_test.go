package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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

func TestLotteryAnnouncementPayloadContainsOnlyRedactedPublicFields(t *testing.T) {
	publicRound := toLotteryPublicRound(LotteryRound{
		ID:                   1,
		RoundNo:              12,
		ParticipantCount:     8,
		ParticipantThreshold: 50,
		PrizeCount:           6,
		PrizeAmount:          5,
		RecentRechargeDays:   30,
	})
	payload, err := json.Marshal(LotteryAnnouncement{
		Enabled:      true,
		CurrentRound: &publicRound,
		RecentWinners: []LotteryWinner{{
			RoundNo: 12,
			Email:   "mh***o@example.com",
		}},
	})
	require.NoError(t, err)
	body := string(payload)
	require.Contains(t, body, `"round_no":12`)
	require.Contains(t, body, `"email":"mh***o@example.com"`)
	require.NotContains(t, body, "recent_recharge_days")
	require.NotContains(t, body, "participant_details")
	require.NotContains(t, body, "balance")
}

func TestLotteryRecentWinnerFeedIncludesMultipleRoundsAtBoundedLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`ORDER BY w\.awarded_at DESC,w\.id DESC LIMIT \$1`).
		WithArgs(lotteryRecentWinnerLimit).
		WillReturnRows(sqlmock.NewRows([]string{"id", "round_id", "round_no", "email_snapshot", "prize_amount", "awarded_at", "joined_at"}).
			AddRow(1, 2, 2, "first@example.com", 5.0, now, now.Add(-time.Hour)).
			AddRow(2, 1, 1, "second@example.com", 5.0, now.Add(-2*time.Hour), now.Add(-3*time.Hour)))

	lottery := NewLotteryService(db, nil, nil)
	winners, err := lottery.listWinners(context.Background(), 0, lotteryRecentWinnerLimit)
	require.NoError(t, err)
	require.Equal(t, 10000, lotteryRecentWinnerLimit)
	require.Equal(t, []int64{2, 1}, []int64{winners[0].RoundNo, winners[1].RoundNo})
	require.Equal(t, "fi***t@example.com", winners[0].Email)
	require.Equal(t, "se***d@example.com", winners[1].Email)
	require.NoError(t, mock.ExpectationsWereMet())
}
