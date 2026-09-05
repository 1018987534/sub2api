package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorRepositoryUpdateSortOrdersUsesOneBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &channelMonitorRepository{db: db}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM channel_monitors WHERE id = ANY($1)")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec("UPDATE channel_monitors[\\s\\S]+SET sort_order = CASE id").
		WithArgs(int64(11), 10, int64(7), 20, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	err = repo.UpdateSortOrders(context.Background(), []service.ChannelMonitorSortOrderUpdate{{ID: 11, SortOrder: 10}, {ID: 7, SortOrder: 20}})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorRepositoryUpdateSortOrdersRejectsMissingIDBeforeUpdate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &channelMonitorRepository{db: db}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM channel_monitors WHERE id = ANY($1)")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	err = repo.UpdateSortOrders(context.Background(), []service.ChannelMonitorSortOrderUpdate{{ID: 11, SortOrder: 10}, {ID: 999, SortOrder: 20}})
	require.ErrorIs(t, err, service.ErrChannelMonitorNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
