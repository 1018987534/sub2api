package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountFirstTokenStatsCache struct {
	stats map[int64]service.FirstTokenLatencyStats
}

func (c accountFirstTokenStatsCache) RecordSample(context.Context, int64, string, int) error {
	return nil
}

func (c accountFirstTokenStatsCache) GetStatsBatch(context.Context, []int64) (map[int64]service.FirstTokenLatencyStats, error) {
	return c.stats, nil
}

func (c accountFirstTokenStatsCache) TryClaimProbe(context.Context, int64, time.Duration) (bool, error) {
	return false, nil
}

func TestAccountHandlerGetFirstTokenLatenciesExcludesOAuthDisabledAndUnschedulableAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	adminSvc := newStubAdminService()
	adminSvc.accountSchedulerScoreFilterAccounts = []service.Account{
		{ID: 1, Name: "relay", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true},
		{ID: 2, Name: "oauth", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true},
		{ID: 3, Name: "disabled", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusDisabled, Schedulable: true},
		{ID: 4, Name: "unschedulable", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: false},
	}
	rateLimitService := service.NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitService.SetFirstTokenLatencyStatsCache(accountFirstTokenStatsCache{stats: map[int64]service.FirstTokenLatencyStats{
		1: {PredictedMS: 4_000, SampleCount: 8, UpdatedAt: now},
		2: {PredictedMS: 1_000, SampleCount: 8, UpdatedAt: now},
		3: {PredictedMS: 2_000, SampleCount: 8, UpdatedAt: now},
		4: {PredictedMS: 3_000, SampleCount: 8, UpdatedAt: now},
	}})
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, rateLimitService, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/api/v1/admin/accounts/first-token-latencies", handler.GetFirstTokenLatencies)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/first-token-latencies", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, adminSvc.schedulerScoreFilterCalls)
	var payload struct {
		Data struct {
			Items []service.AccountFirstTokenLatencyMetric `json:"items"`
			Total int                                      `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, 1, payload.Data.Total)
	require.Len(t, payload.Data.Items, 1)
	require.Equal(t, int64(1), payload.Data.Items[0].AccountID)
	require.Equal(t, "relay", payload.Data.Items[0].AccountName)
}
