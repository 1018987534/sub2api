package service

import (
	"context"
	"fmt"
	"time"
)

// Read the ledger written atomically with the award. This includes historical
// prizes without crediting balances again or counting rewards as recharge.
func (s *adminServiceImpl) listLotteryBalanceHistory(ctx context.Context, userID int64, offset, limit int) ([]RedeemCode, int64, error) {
	if s == nil || s.entClient == nil || userID <= 0 || limit <= 0 {
		return nil, 0, nil
	}
	countRows, err := s.entClient.QueryContext(ctx, `SELECT COUNT(*) FROM lottery_balance_ledger WHERE user_id = $1`, userID)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if countRows.Next() {
		err = countRows.Scan(&total)
	}
	rowsErr := countRows.Err()
	_ = countRows.Close()
	if err != nil {
		return nil, 0, err
	}
	if rowsErr != nil {
		return nil, 0, rowsErr
	}
	if total == 0 {
		return []RedeemCode{}, 0, nil
	}

	rows, err := s.entClient.QueryContext(ctx, `
SELECT l.id, r.round_no, l.amount::double precision,
       l.balance_before::double precision, l.balance_after::double precision, l.created_at
FROM lottery_balance_ledger l
JOIN lottery_rounds r ON r.id = l.round_id
WHERE l.user_id = $1
ORDER BY l.created_at DESC, l.id DESC
OFFSET $2 LIMIT $3`, userID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	codes := make([]RedeemCode, 0)
	for rows.Next() {
		var id, roundNo int64
		var amount, before, after float64
		var awardedAt time.Time
		if err := rows.Scan(&id, &roundNo, &amount, &before, &after, &awardedAt); err != nil {
			return nil, 0, err
		}
		codes = append(codes, RedeemCode{
			ID: -id, Code: fmt.Sprintf("LOT-%d", id), Type: RedeemTypeLotteryReward,
			Value: amount, Status: StatusUsed, UsedBy: &userID, UsedAt: &awardedAt, CreatedAt: awardedAt,
			LotteryRoundNo: roundNo, BalanceBefore: &before, BalanceAfter: &after,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return codes, total, nil
}
