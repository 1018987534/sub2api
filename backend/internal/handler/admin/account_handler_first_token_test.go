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
	stats           map[int64]service.FirstTokenLatencyStats
	manualRequested int64
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

func (c *accountFirstTokenStatsCache) RequestManualProbe(_ context.Context, accountID int64, _ time.Duration) error {
	c.manualRequested = accountID
	return nil
}

func (c *accountFirstTokenStatsCache) TryClaimManualProbe(context.Context, []int64, time.Duration) (int64, bool, error) {
	return 0, false, nil
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

func TestAccountHandlerGetFirstTokenPoolStatusesAggregatesGroupsWithoutAccountDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	plus := &service.Group{ID: 5, Name: "PLUS分组", Platform: service.PlatformOpenAI, Status: service.StatusActive}
	pro := &service.Group{ID: 79, Name: "PRO分组", Platform: service.PlatformOpenAI, Status: service.StatusActive}
	special := &service.Group{ID: 80, Name: "特价分组", Platform: service.PlatformOpenAI, Status: service.StatusActive}
	fast := service.Account{
		ID: 1, Name: "fast-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		GroupIDs: []int64{plus.ID, pro.ID}, Groups: []*service.Group{plus, pro},
		AccountGroups: []service.AccountGroup{
			{AccountID: 1, GroupID: plus.ID, Group: plus},
			{AccountID: 1, GroupID: pro.ID, Group: pro},
		},
	}
	slow := service.Account{
		ID: 2, Name: "slow-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		GroupIDs: []int64{special.ID}, Groups: []*service.Group{special},
		AccountGroups: []service.AccountGroup{{AccountID: 2, GroupID: special.ID, Group: special}},
	}
	adminSvc := newStubAdminService()
	adminSvc.accountSchedulerScoreFilterAccounts = []service.Account{fast, slow}
	rateLimitService := service.NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitService.SetFirstTokenLatencyStatsCache(accountFirstTokenStatsCache{stats: map[int64]service.FirstTokenLatencyStats{
		1: {PredictedMS: 7_000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
		2: {PredictedMS: 20_000, SampleCount: 20, UpdatedAt: now, FastConfirmationTracked: true},
	}})
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, rateLimitService, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/api/v1/groups/pool-status", handler.GetFirstTokenPoolStatuses)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/groups/pool-status", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, 3, payload.Data.Total)
	require.Equal(t, []map[string]any{
		{"group_id": float64(5), "group_name": "PLUS分组", "is_available": true},
		{"group_id": float64(79), "group_name": "PRO分组", "is_available": true},
		{"group_id": float64(80), "group_name": "特价分组", "is_available": false},
	}, payload.Data.Items)
	require.NotContains(t, recorder.Body.String(), "fast-account")
	require.NotContains(t, recorder.Body.String(), "predicted_ms")
}

func TestAccountHandlerRequestsFirstTokenManualProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := newStubAdminService()
	adminSvc.getAccountResult = &service.Account{
		ID: 42, Name: "relay", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
	}
	cache := &accountFirstTokenStatsCache{}
	rateLimitService := service.NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitService.SetFirstTokenLatencyStatsCache(cache)
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, rateLimitService, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/api/v1/admin/accounts/:id/first-token-probe", handler.RequestFirstTokenManualProbe)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/first-token-probe", nil))

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, int64(42), cache.manualRequested)
	var payload struct {
		Data struct {
			AccountID int64 `json:"account_id"`
			Queued    bool  `json:"queued"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, int64(42), payload.Data.AccountID)
	require.True(t, payload.Data.Queued)
}
