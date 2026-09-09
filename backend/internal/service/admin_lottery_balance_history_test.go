package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type lotteryHistoryRedeemRepo struct {
	RedeemCodeRepository
	t *testing.T
}

func (r lotteryHistoryRedeemRepo) ListByUserPaginated(_ context.Context, userID int64, _ pagination.PaginationParams, codeType string) ([]RedeemCode, *pagination.PaginationResult, error) {
	require.Equal(r.t, int64(7), userID)
	require.Empty(r.t, codeType)
	return []RedeemCode{{ID: 1, Type: RedeemTypeBalance, Value: 10, CreatedAt: time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)}}, &pagination.PaginationResult{Total: 1}, nil
}

func (r lotteryHistoryRedeemRepo) SumPositiveBalanceByUser(_ context.Context, userID int64) (float64, error) {
	require.Equal(r.t, int64(7), userID)
	return 10, nil
}

func newLotteryHistoryTestService(t *testing.T) (*adminServiceImpl, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return &adminServiceImpl{entClient: client, redeemCodeRepo: lotteryHistoryRedeemRepo{t: t}}, mock
}

func expectLotteryHistory(mock sqlmock.Sqlmock, total int64, offset, limit int) {
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM lottery_balance_ledger WHERE user_id = \\$1").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(total))
	if total == 0 {
		return
	}
	mock.ExpectQuery("FROM lottery_balance_ledger l JOIN lottery_rounds r ON r.id = l.round_id WHERE l.user_id = \\$1 ORDER BY l.created_at DESC, l.id DESC OFFSET \\$2 LIMIT \\$3").
		WithArgs(int64(7), offset, limit).
		WillReturnRows(sqlmock.NewRows([]string{"id", "round_no", "amount", "before", "after", "created_at"}).
			AddRow(19, 10, 5, 10.18850296, 15.18850296, time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC)))
}

func TestUserBalanceHistoryIncludesLotteryWithoutInflatingRecharge(t *testing.T) {
	for _, page := range []int{1, 2} {
		t.Run(map[int]string{1: "first page", 2: "second page"}[page], func(t *testing.T) {
			svc, mock := newLotteryHistoryTestService(t)
			mock.ExpectQuery("FROM user_affiliate_ledger.*ORDER BY").WithArgs(int64(7), 0, 1000).
				WillReturnRows(sqlmock.NewRows([]string{"id", "amount", "created_at"}).
					AddRow(19, 3, time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)))
			mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM user_affiliate_ledger").WithArgs(int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			expectLotteryHistory(mock, 1, 0, page*2)
			items, total, recharged, err := svc.GetUserBalanceHistory(context.Background(), 7, page, 2, "")
			require.NoError(t, err)
			require.Equal(t, int64(3), total)
			require.Equal(t, float64(10), recharged)
			if page == 1 {
				require.Len(t, items, 2)
				require.Equal(t, RedeemTypeLotteryReward, items[0].Type)
				require.Equal(t, int64(10), items[0].LotteryRoundNo)
				require.Equal(t, float64(5), items[0].Value)
				require.Equal(t, 10.18850296, *items[0].BalanceBefore)
				require.Equal(t, 15.18850296, *items[0].BalanceAfter)
				require.Equal(t, RedeemTypeAffiliateBalance, items[1].Type)
			} else {
				require.Len(t, items, 1)
				require.Equal(t, RedeemTypeBalance, items[0].Type)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUserBalanceHistoryLotteryFilterAndEmptyUser(t *testing.T) {
	for _, count := range []int64{0, 3} {
		t.Run(map[int64]string{0: "no awards", 3: "historical award on page two"}[count], func(t *testing.T) {
			svc, mock := newLotteryHistoryTestService(t)
			expectLotteryHistory(mock, count, 2, 2)
			items, total, recharged, err := svc.GetUserBalanceHistory(context.Background(), 7, 2, 2, RedeemTypeLotteryReward)
			require.NoError(t, err)
			require.Equal(t, count, total)
			require.Equal(t, float64(10), recharged)
			if count == 0 {
				require.Empty(t, items)
			} else {
				require.Len(t, items, 1)
				require.Equal(t, int64(7), *items[0].UsedBy)
				require.Equal(t, StatusUsed, items[0].Status)
				require.Equal(t, items[0].CreatedAt, *items[0].UsedAt)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUserBalanceHistoryLotteryQueryFailureIsNotHidden(t *testing.T) {
	svc, mock := newLotteryHistoryTestService(t)
	mock.ExpectQuery("SELECT COUNT").WithArgs(int64(7)).WillReturnError(errors.New("database unavailable"))
	_, _, _, err := svc.GetUserBalanceHistory(context.Background(), 7, 1, 15, RedeemTypeLotteryReward)
	require.ErrorContains(t, err, "database unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}
