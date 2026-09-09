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
