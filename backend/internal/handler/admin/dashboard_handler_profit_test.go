package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dashboardProfitHandlerRepo struct {
	service.UsageLogRepository
	calls int
	err   error
}

func (r *dashboardProfitHandlerRepo) GetDashboardStats(context.Context) (*usagestats.DashboardStats, error) {
	return &usagestats.DashboardStats{TodayActualCost: 315.88, TodayAccountCost: 202, TodayLotteryCost: 5, TodayQQRewardCost: 4,
		MonthActualCost: 1000, MonthAccountCost: 600, MonthLotteryCost: 10, MonthQQRewardCost: 20}, nil
}

func (r *dashboardProfitHandlerRepo) GetBatchUserUsageStats(_ context.Context, ids []int64, _, _ time.Time) (map[int64]*usagestats.BatchUserUsageStats, error) {
	r.calls++
	return map[int64]*usagestats.BatchUserUsageStats{
		ids[0]: {UserID: ids[0], TodayActualCost: float64(ids[0]) * 10, TotalActualCost: float64(ids[0]) * 50},
	}, r.err
}

func newDashboardProfitRouter(repo *dashboardProfitHandlerRepo, adminID *int64, settings ...*service.SettingService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	var settingService *service.SettingService
	if len(settings) > 0 {
		settingService = settings[0]
	}
	handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil, settingService), nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if *adminID > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: *adminID})
		}
	})
	router.GET("/stats", handler.GetStats)
	router.GET("/snapshot", handler.GetSnapshotV2)
	return router
}

func TestDashboardHandler_ProfitSnapshotCacheIsolatedByAuthenticatedAdmin(t *testing.T) {
	resetDashboardReadCachesForTest()
	t.Cleanup(resetDashboardReadCachesForTest)
	repo := &dashboardProfitHandlerRepo{}
	adminID := int64(2)
	router := newDashboardProfitRouter(repo, &adminID)
	const url = "/snapshot?include_trend=false&include_model_stats=false&user_id=999"
	request := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("If-None-Match", etag)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := request("")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var body struct {
		Data struct {
			Stats struct {
				usagestats.DashboardStats
				usagestats.DashboardProfitStats
			} `json:"stats"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &body))
	require.Equal(t, 20.0, body.Data.Stats.TodayAdminActualCost)
	require.Equal(t, 5.0, body.Data.Stats.TodayLotteryCost)
	require.Equal(t, 4.0, body.Data.Stats.TodayQQRewardCost)
	require.InDelta(t, 5.32584, body.Data.Stats.TodayWithdrawalFee, 1e-9)
	require.InDelta(t, 79.55416, body.Data.Stats.TodayProfit, 1e-9)
	require.InDelta(t, 253.8, body.Data.Stats.MonthProfit, 1e-9)
	require.Equal(t, 100.0, body.Data.Stats.MonthAdminActualCost)
	require.Equal(t, "miss", first.Header().Get("X-Snapshot-Cache"))
	require.Equal(t, http.StatusNotModified, request(first.Header().Get("ETag")).Code)
	require.Equal(t, 1, repo.calls)

	adminID = 3
	second := request(first.Header().Get("ETag"))
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &body))
	require.Equal(t, 30.0, body.Data.Stats.TodayAdminActualCost)
	require.Equal(t, 5.0, body.Data.Stats.TodayLotteryCost)
	require.Equal(t, 4.0, body.Data.Stats.TodayQQRewardCost)
	require.InDelta(t, 5.14584, body.Data.Stats.TodayWithdrawalFee, 1e-9)
	require.InDelta(t, 69.73416, body.Data.Stats.TodayProfit, 1e-9)
	require.InDelta(t, 204.7, body.Data.Stats.MonthProfit, 1e-9)
	require.NotEqual(t, first.Header().Get("ETag"), second.Header().Get("ETag"))
	require.Equal(t, 2, repo.calls)
}

func TestDashboardHandler_ProfitStatsAndFailureHandling(t *testing.T) {
	resetDashboardReadCachesForTest()
	t.Cleanup(resetDashboardReadCachesForTest)
	repo := &dashboardProfitHandlerRepo{}
	adminID := int64(2)
	router := newDashboardProfitRouter(repo, &adminID)
	for _, url := range []string{"/stats", "/snapshot?include_trend=false&include_model_stats=false"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), `"today_admin_actual_cost":20`)
		require.Contains(t, rec.Body.String(), `"today_account_cost":202`)
		require.Contains(t, rec.Body.String(), `"today_lottery_cost":5`)
		require.Contains(t, rec.Body.String(), `"today_qq_reward_cost":4`)
		require.Contains(t, rec.Body.String(), `"today_server_cost_usd":0`)
		require.Contains(t, rec.Body.String(), `"month_actual_cost":1000`)
		require.Contains(t, rec.Body.String(), `"month_account_cost":600`)
		require.Contains(t, rec.Body.String(), `"month_lottery_cost":10`)
		require.Contains(t, rec.Body.String(), `"month_qq_reward_cost":20`)
		var body struct {
			Data struct {
				Stats *usagestats.DashboardProfitStats `json:"stats"`
				usagestats.DashboardProfitStats
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		profit := body.Data.DashboardProfitStats
		if body.Data.Stats != nil {
			profit = *body.Data.Stats
		}
		require.InDelta(t, 5.32584, profit.TodayWithdrawalFee, 1e-9)
		require.InDelta(t, 79.55416, profit.TodayProfit, 1e-9)
		require.InDelta(t, 253.8, profit.MonthProfit, 1e-9)

		adminID = 0
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		adminID = 2
	}

	repo.err = errors.New("self usage unavailable")
	resetDashboardReadCachesForTest()
	for _, url := range []string{"/stats", "/snapshot?include_trend=false&include_model_stats=false"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.NotContains(t, rec.Body.String(), `"today_profit"`)
	}
}

func TestDashboardHandler_ProfitServerCostSaveRefreshesSnapshotImmediately(t *testing.T) {
	resetDashboardReadCachesForTest()
	t.Cleanup(resetDashboardReadCachesForTest)
	settingsRepo := &dashboardProfitHandlerSettingsRepo{}
	settingsService := service.NewSettingService(settingsRepo, nil)
	adminID := int64(2)
	router := newDashboardProfitRouter(&dashboardProfitHandlerRepo{}, &adminID, settingsService)
	settingsHandler := NewSettingHandler(settingsService, nil, nil, nil, nil, nil, nil)
	router.PUT("/settings/dashboard-profit", settingsHandler.UpdateDashboardProfitSettings)
	request := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/snapshot?include_trend=false&include_model_stats=false", nil)
		req.Header.Set("If-None-Match", etag)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := request("")
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusNotModified, request(first.Header().Get("ETag")).Code)
	updated := dashboardProfitSettingsRequest(router, http.MethodPut, `{"monthly_server_cost_usd":600}`)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	var saved struct {
		Data service.DashboardProfitSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &saved))
	second := request(first.Header().Get("ETag"))
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "miss", second.Header().Get("X-Snapshot-Cache"))
	var body struct {
		Data struct {
			Stats usagestats.DashboardProfitStats `json:"stats"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &body))
	require.InDelta(t, saved.Data.DailyServerCostUSD, body.Data.Stats.TodayServerCostUSD, 1e-9)
	require.InDelta(t, 79.55416-saved.Data.DailyServerCostUSD, body.Data.Stats.TodayProfit, 1e-9)
	require.Greater(t, body.Data.Stats.MonthServerCostUSD, 0.0)
	require.InDelta(t, 253.8-body.Data.Stats.MonthServerCostUSD, body.Data.Stats.MonthProfit, 1e-9)
	require.NotEqual(t, first.Header().Get("ETag"), second.Header().Get("ETag"))
	cleared := dashboardProfitSettingsRequest(router, http.MethodPut, `{"monthly_server_cost_usd":0}`)
	require.Equal(t, http.StatusOK, cleared.Code)
	third := request(second.Header().Get("ETag"))
	require.Equal(t, http.StatusOK, third.Code)
	require.NoError(t, json.Unmarshal(third.Body.Bytes(), &body))
	require.Zero(t, body.Data.Stats.TodayServerCostUSD)
	require.InDelta(t, 79.55416, body.Data.Stats.TodayProfit, 1e-9)
	require.InDelta(t, 253.8, body.Data.Stats.MonthProfit, 1e-9)
	settingsRepo.getErr = errors.New("settings unavailable")
	require.Equal(t, http.StatusInternalServerError, request("").Code, "must not serve cached profit with an unknown server expense")
}
