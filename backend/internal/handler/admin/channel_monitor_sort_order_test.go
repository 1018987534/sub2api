//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type channelMonitorSortOrderHandlerRepoStub struct {
	service.ChannelMonitorRepository
	updates []service.ChannelMonitorSortOrderUpdate
}

func (r *channelMonitorSortOrderHandlerRepoStub) UpdateSortOrders(_ context.Context, updates []service.ChannelMonitorSortOrderUpdate) error {
	r.updates = append([]service.ChannelMonitorSortOrderUpdate(nil), updates...)
	return nil
}

func setupChannelMonitorSortOrderRouter() (*gin.Engine, *channelMonitorSortOrderHandlerRepoStub) {
	repo := &channelMonitorSortOrderHandlerRepoStub{}
	handler := NewChannelMonitorHandler(service.NewChannelMonitorService(repo, nil))

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/api/v1/admin/channel-monitors/sort-order", handler.UpdateSortOrder)
	return router, repo
}

func TestChannelMonitorSortOrderHandlerUpdatesCompleteOrder(t *testing.T) {
	router, repo := setupChannelMonitorSortOrderRouter()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/channel-monitors/sort-order",
		strings.NewReader(`{"ordered_ids":[9,4,7]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"message":"Sort order updated successfully"}}`, recorder.Body.String())
	require.Equal(t, []service.ChannelMonitorSortOrderUpdate{
		{ID: 9, SortOrder: 10},
		{ID: 4, SortOrder: 20},
		{ID: 7, SortOrder: 30},
	}, repo.updates)
}

func TestChannelMonitorSortOrderHandlerRejectsDuplicateIDs(t *testing.T) {
	router, repo := setupChannelMonitorSortOrderRouter()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/channel-monitors/sort-order",
		strings.NewReader(`{"ordered_ids":[9,4,9]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"reason":"CHANNEL_MONITOR_INVALID_SORT_ORDER"`)
	require.Empty(t, repo.updates)
}
