package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDashboardProfitSettingsHandlerWithdrawalFeePartialUpdates(t *testing.T) {
	repo := &dashboardProfitHandlerSettingsRepo{}
	router := dashboardProfitSettingsTestRouter(repo)
	for _, body := range []string{
		"{\"monthly_server_cost_usd\":265.31,\"withdrawal_fee_rate_percent\":3.2}",
		"{\"monthly_server_cost_usd\":600}",
		"{\"withdrawal_fee_rate_percent\":0}",
	} {
		result := dashboardProfitSettingsRequest(router, http.MethodPut, body)
		require.Equal(t, http.StatusOK, result.Code, result.Body.String())
	}
	readback := dashboardProfitSettingsRequest(dashboardProfitSettingsTestRouter(repo), http.MethodGet, "")
	require.Equal(t, http.StatusOK, readback.Code)
	var body struct {
		Data service.DashboardProfitSettings
	}
	require.NoError(t, json.Unmarshal(readback.Body.Bytes(), &body))
	require.Equal(t, 600.0, body.Data.MonthlyServerCostUSD)
	require.Zero(t, body.Data.WithdrawalFeeRatePercent)
}

func TestDashboardProfitSettingsHandlerRejectsInvalidWithdrawalFees(t *testing.T) {
	for _, body := range []string{
		"{\"withdrawal_fee_rate_percent\":-1}",
		"{\"withdrawal_fee_rate_percent\":100.01}",
		"{\"withdrawal_fee_rate_percent\":\"3.2\"}",
		"{\"withdrawal_fee_rate_percent\":null}",
		"{\"withdrawal_fee_rate_percent\":1e999}",
		"{\"monthly_server_cost_usd\":600,\"withdrawal_fee_rate_percent\":-1}",
	} {
		t.Run(body, func(t *testing.T) {
			repo := &dashboardProfitHandlerSettingsRepo{}
			result := dashboardProfitSettingsRequest(dashboardProfitSettingsTestRouter(repo), http.MethodPut, body)
			require.Equal(t, http.StatusBadRequest, result.Code, result.Body.String())
			require.Zero(t, repo.writes)
		})
	}
}

func TestDashboardHandler_WithdrawalFeeSaveRefreshesCachedSnapshotImmediately(t *testing.T) {
	resetDashboardReadCachesForTest()
	t.Cleanup(resetDashboardReadCachesForTest)
	settingsRepo := &dashboardProfitHandlerSettingsRepo{}
	settingsService := service.NewSettingService(settingsRepo, nil)
	adminID := int64(2)
	router := newDashboardProfitRouter(&dashboardProfitHandlerRepo{}, &adminID, settingsService)
	h := NewSettingHandler(settingsService, nil, nil, nil, nil, nil, nil)
	router.PUT("/settings/dashboard-profit", h.UpdateDashboardProfitSettings)
	request := func(url, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("If-None-Match", etag)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	const url = "/snapshot?include_trend=false&include_model_stats=false"
	first := request(url, "")
	require.Equal(t, http.StatusOK, first.Code)
	etag := first.Header().Get("ETag")
	require.Equal(t, http.StatusNotModified, request(url, etag).Code)
	for _, tc := range []struct{ rate, todayFee, monthFee, todayProfit, monthProfit float64 }{
		{3.2, 9.46816, 28.8, 75.41184, 241.2},
		{0, 0, 0, 84.88, 270},
	} {
		payload := fmt.Sprintf("{\"withdrawal_fee_rate_percent\":%g}", tc.rate)
		updated := dashboardProfitSettingsRequest(router, http.MethodPut, payload)
		require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
		response := request(url, etag)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "miss", response.Header().Get("X-Snapshot-Cache"))
		require.NotEqual(t, etag, response.Header().Get("ETag"))
		etag = response.Header().Get("ETag")
		var snapshot struct {
			Data struct {
				Stats usagestats.DashboardProfitStats
			}
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &snapshot))
		profit := snapshot.Data.Stats
		require.Equal(t, tc.rate, profit.WithdrawalFeeRatePercent)
		require.InDelta(t, tc.todayFee, profit.TodayWithdrawalFee, 1e-9)
		require.InDelta(t, tc.monthFee, profit.MonthWithdrawalFee, 1e-9)
		require.InDelta(t, tc.todayProfit, profit.TodayProfit, 1e-9)
		require.InDelta(t, tc.monthProfit, profit.MonthProfit, 1e-9)
		require.Equal(t, http.StatusNotModified, request(url, etag).Code)
		var direct struct {
			Data usagestats.DashboardProfitStats
		}
		require.NoError(t, json.Unmarshal(request("/stats", "").Body.Bytes(), &direct))
		require.Equal(t, tc.rate, direct.Data.WithdrawalFeeRatePercent)
		require.InDelta(t, tc.todayFee, direct.Data.TodayWithdrawalFee, 1e-9)
	}
}
