package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dashboardProfitHandlerSettingsRepo struct {
	service.SettingRepository
	values map[string]string
	getErr error
	setErr error
	writes int
}

func (r *dashboardProfitHandlerSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, r.getErr
}

func (r *dashboardProfitHandlerSettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range values {
		r.values[key] = value
	}
	r.writes++
	return nil
}

func dashboardProfitSettingsTestRouter(repo *dashboardProfitHandlerSettingsRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewSettingHandler(service.NewSettingService(repo, nil), nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/settings/dashboard-profit", h.GetDashboardProfitSettings)
	router.PUT("/settings/dashboard-profit", h.UpdateDashboardProfitSettings)
	return router
}

func dashboardProfitSettingsRequest(router *gin.Engine, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/settings/dashboard-profit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestDashboardProfitSettingsHandlerPersistsReadsAndClears(t *testing.T) {
	repo := &dashboardProfitHandlerSettingsRepo{}
	initial := dashboardProfitSettingsRequest(dashboardProfitSettingsTestRouter(repo), http.MethodGet, "")
	require.Equal(t, http.StatusOK, initial.Code)
	require.Contains(t, initial.Body.String(), `"monthly_server_cost_usd":0`)
	for _, monthly := range []string{"600", "620.5", "0"} {
		updated := dashboardProfitSettingsRequest(dashboardProfitSettingsTestRouter(repo), http.MethodPut, `{"monthly_server_cost_usd":`+monthly+`}`)
		require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
		readback := dashboardProfitSettingsRequest(dashboardProfitSettingsTestRouter(repo), http.MethodGet, "")
		require.Equal(t, http.StatusOK, readback.Code)
		require.Contains(t, readback.Body.String(), `"monthly_server_cost_usd":`+monthly)
		require.Contains(t, readback.Body.String(), `"daily_server_cost_usd":`)
		require.Contains(t, readback.Body.String(), `"month_days":`)
	}
}

func TestDashboardProfitSettingsHandlerRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{`{}`, `{"monthly_server_cost_usd":null}`, `{"monthly_server_cost_usd":-1}`, `{"monthly_server_cost_usd":"600"}`, `{"monthly_server_cost_usd":1e999}`, `{`} {
		t.Run(body, func(t *testing.T) {
			repo := &dashboardProfitHandlerSettingsRepo{}
			result := dashboardProfitSettingsRequest(dashboardProfitSettingsTestRouter(repo), http.MethodPut, body)
			require.Equal(t, http.StatusBadRequest, result.Code)
			require.Zero(t, repo.writes)
		})
	}
}

func TestDashboardProfitSettingsHandlerReportsStorageFailures(t *testing.T) {
	repo := &dashboardProfitHandlerSettingsRepo{setErr: errors.New("write failed")}
	router := dashboardProfitSettingsTestRouter(repo)
	require.Equal(t, http.StatusInternalServerError, dashboardProfitSettingsRequest(router, http.MethodPut, `{"monthly_server_cost_usd":600}`).Code)
	repo.getErr = errors.New("read failed")
	require.Equal(t, http.StatusInternalServerError, dashboardProfitSettingsRequest(router, http.MethodGet, "").Code)
}
