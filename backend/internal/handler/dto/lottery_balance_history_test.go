package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLotteryBalanceHistoryAdminDTO(t *testing.T) {
	before, after := 0.0, 5.0
	code := &service.RedeemCode{Type: service.RedeemTypeLotteryReward, Value: 5, LotteryRoundNo: 10, BalanceBefore: &before, BalanceAfter: &after}
	body, err := json.Marshal(RedeemCodeFromServiceAdmin(code))
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(body, &fields))
	require.Equal(t, float64(10), fields["lottery_round_no"])
	require.Equal(t, float64(0), fields["balance_before"])
	require.Equal(t, float64(5), fields["balance_after"])

	body, err = json.Marshal(RedeemCodeFromServiceAdmin(&service.RedeemCode{Type: service.RedeemTypeBalance}))
	require.NoError(t, err)
	require.NotContains(t, string(body), "lottery_round_no")
	require.NotContains(t, string(body), "balance_before")
}

func TestLotteryBalanceHistoryUserDTO(t *testing.T) {
	before, after := 10.0, 15.0
	code := &service.RedeemCode{
		Type: service.RedeemTypeLotteryReward, Value: 5, LotteryRoundNo: 22,
		BalanceBefore: &before, BalanceAfter: &after, Notes: "internal award details",
	}
	body, err := json.Marshal(RedeemCodeFromService(code))
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(body, &fields))
	require.Equal(t, float64(22), fields["lottery_round_no"])
	require.Equal(t, service.RedeemTypeLotteryReward, fields["type"])
	require.Equal(t, float64(5), fields["value"])
	require.NotContains(t, fields, "balance_before")
	require.NotContains(t, fields, "balance_after")
	require.NotContains(t, fields, "notes")
	body, err = json.Marshal(RedeemCodeFromService(&service.RedeemCode{Type: service.RedeemTypeBalance}))
	require.NoError(t, err)
	require.NotContains(t, string(body), "lottery_round_no")
}
