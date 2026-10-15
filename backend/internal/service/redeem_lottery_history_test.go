package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type personalHistoryRepo struct {
	RedeemCodeRepository
	items []RedeemCode
	err   error
}

func (r personalHistoryRepo) ListByUser(_ context.Context, _ int64, limit int) ([]RedeemCode, error) {
	if limit > len(r.items) {
		limit = len(r.items)
	}
	return r.items[:limit], r.err
}

func (r personalHistoryRepo) ListByUserPaginated(_ context.Context, _ int64, params pagination.PaginationParams, _ string) ([]RedeemCode, *pagination.PaginationResult, error) {
	start := min(params.Offset(), len(r.items))
	end := min(start+params.Limit(), len(r.items))
	return append([]RedeemCode{}, r.items[start:end]...), &pagination.PaginationResult{Total: int64(len(r.items))}, r.err
}

func newPersonalHistoryService(t *testing.T, repo personalHistoryRepo) (*RedeemService, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return NewRedeemService(repo, nil, nil, nil, nil, client, nil, nil), mock
}

func TestPersonalLotteryHistory(t *testing.T) {
	repo := personalHistoryRepo{items: []RedeemCode{{ID: 1, Type: RedeemTypeBalance, Value: 10, CreatedAt: time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)}}}
	svc, mock := newPersonalHistoryService(t, repo)
	expectLotteryHistory(mock, 1, 0, 20)
	items, result, err := svc.GetUserHistoryPaginated(context.Background(), 7, pagination.PaginationParams{Page: 1, PageSize: 20})
	require.NoError(t, err)
	t.Logf("personal history: total=%d rows=%d newest_type=%s newest_value=%.2f", result.Total, len(items), items[0].Type, items[0].Value)
	require.Equal(t, int64(2), result.Total)
	require.Len(t, items, 2)
	require.Equal(t, RedeemTypeLotteryReward, items[0].Type)
	require.Equal(t, int64(10), items[0].LotteryRoundNo)
	require.Equal(t, int64(7), *items[0].UsedBy)
	require.Equal(t, float64(5), items[0].Value)
	require.Equal(t, "LOT-19", items[0].Code)
	require.Equal(t, RedeemTypeBalance, items[1].Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPersonalLotteryHistoryLegacy(t *testing.T) {
	svc, mock := newPersonalHistoryService(t, personalHistoryRepo{})
	expectLotteryHistory(mock, 1, 0, 25)
	items, err := svc.GetUserHistory(context.Background(), 7, 25)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, RedeemTypeLotteryReward, items[0].Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPersonalLotteryHistoryPagination(t *testing.T) {
	for _, page := range []int{1, 2, 3, 1002} {
		t.Run("page="+strconv.Itoa(page), func(t *testing.T) {
			older := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
			newer := older.Add(4 * time.Hour)
			items := []RedeemCode{{ID: 2, Type: RedeemTypeSubscription, UsedAt: &newer}, {ID: 1, Type: RedeemTypeBalance, CreatedAt: older}}
			if page > 1000 {
				items = make([]RedeemCode, 1100)
				for i := range items {
					items[i] = RedeemCode{ID: int64(1100 - i), Type: RedeemTypeBalance, CreatedAt: older}
				}
			}
			svc, mock := newPersonalHistoryService(t, personalHistoryRepo{items: items})
			expectLotteryHistory(mock, 1, 0, page)
			got, result, err := svc.GetUserHistoryPaginated(context.Background(), 7, pagination.PaginationParams{Page: page, PageSize: 1})
			require.NoError(t, err)
			require.Equal(t, int64(len(items)+1), result.Total)
			require.Equal(t, len(items)+1, result.Pages)
			require.Equal(t, page, result.Page)
			require.Equal(t, 1, result.PageSize)
			require.Len(t, got, 1)
			if page <= 3 {
				require.Equal(t, []string{RedeemTypeSubscription, RedeemTypeLotteryReward, RedeemTypeBalance}[page-1], got[0].Type)
			} else {
				require.Equal(t, int64(100), got[0].ID)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPersonalLotteryHistoryEmptyAndPastLastPage(t *testing.T) {
	for _, page := range []int{1, 10} {
		svc, mock := newPersonalHistoryService(t, personalHistoryRepo{})
		expectLotteryHistory(mock, 0, 0, page*20)
		items, result, err := svc.GetUserHistoryPaginated(context.Background(), 7, pagination.PaginationParams{Page: page, PageSize: 20})
		require.NoError(t, err)
		require.NotNil(t, items)
		require.Empty(t, items)
		require.Zero(t, result.Total)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestPersonalLotteryHistoryDoesNotHideDatabaseErrors(t *testing.T) {
	svc, mock := newPersonalHistoryService(t, personalHistoryRepo{})
	mock.ExpectQuery("SELECT COUNT").WithArgs(int64(7)).WillReturnError(errors.New("ledger unavailable"))
	_, _, err := svc.GetUserHistoryPaginated(context.Background(), 7, pagination.PaginationParams{Page: 1, PageSize: 20})
	require.ErrorContains(t, err, "ledger unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
	svc, mock = newPersonalHistoryService(t, personalHistoryRepo{err: errors.New("redeem unavailable")})
	expectLotteryHistory(mock, 0, 0, 20)
	_, _, err = svc.GetUserHistoryPaginated(context.Background(), 7, pagination.PaginationParams{Page: 1, PageSize: 20})
	require.ErrorContains(t, err, "redeem unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}
