package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type channelMonitorSortOrderRepoStub struct {
	ChannelMonitorRepository
	updates []ChannelMonitorSortOrderUpdate
}

func (repo *channelMonitorSortOrderRepoStub) UpdateSortOrders(_ context.Context, updates []ChannelMonitorSortOrderUpdate) error {
	repo.updates = append([]ChannelMonitorSortOrderUpdate(nil), updates...)
	return nil
}

func TestChannelMonitorServiceUpdateSortOrdersNormalizesPositions(t *testing.T) {
	repo := &channelMonitorSortOrderRepoStub{}
	svc := NewChannelMonitorService(repo, nil)

	require.NoError(t, svc.UpdateSortOrders(context.Background(), []int64{12, 7, 30}))
	require.Equal(t, []ChannelMonitorSortOrderUpdate{
		{ID: 12, SortOrder: 10},
		{ID: 7, SortOrder: 20},
		{ID: 30, SortOrder: 30},
	}, repo.updates)
}

func TestChannelMonitorServiceUpdateSortOrdersRejectsInvalidIDs(t *testing.T) {
	for _, ids := range [][]int64{nil, {4, 4}, {4, 0}} {
		repo := &channelMonitorSortOrderRepoStub{}
		svc := NewChannelMonitorService(repo, nil)
		require.ErrorIs(t, svc.UpdateSortOrders(context.Background(), ids), ErrChannelMonitorInvalidSortOrder)
		require.Empty(t, repo.updates)
	}
}
