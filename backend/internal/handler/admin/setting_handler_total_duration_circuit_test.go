package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type totalDurationCircuitHandlerRepo struct {
	service.SettingRepository
	value  string
	getErr error
	setErr error
}

func (r *totalDurationCircuitHandlerRepo) GetValue(context.Context, string) (string, error) {
	return r.value, r.getErr
}

func (r *totalDurationCircuitHandlerRepo) Set(_ context.Context, _, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	r.value = value
	return nil
}

func totalDurationCircuitTestRouter(repo *totalDurationCircuitHandlerRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewSettingHandler(service.NewSettingService(repo, nil), nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/settings/total-duration-circuit-breaker", h.GetTotalDurationCircuitBreakerSettings)
	router.PUT("/settings/total-duration-circuit-breaker", h.UpdateTotalDurationCircuitBreakerSettings)
	return router
}

func circuitSettingsRequest(router *gin.Engine, method, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/settings/total-duration-circuit-breaker", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestTotalDurationCircuitSettingsHandlerPersistsAndReadsBack(t *testing.T) {
	repo := &totalDurationCircuitHandlerRepo{}
	router := totalDurationCircuitTestRouter(repo)
	initial := circuitSettingsRequest(router, http.MethodGet, "")
	require.Equal(t, http.StatusOK, initial.Code)
	require.Contains(t, initial.Body.String(), `"max_slow_requests":15`)

	updated := circuitSettingsRequest(router, http.MethodPut, `{"max_slow_requests":7}`)
	require.Equal(t, http.StatusOK, updated.Code)
	require.Equal(t, "8", repo.value)
	readback := circuitSettingsRequest(totalDurationCircuitTestRouter(repo), http.MethodGet, "")
	require.Equal(t, http.StatusOK, readback.Code)
	var result struct {
		Data service.TotalDurationCircuitBreakerSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(readback.Body.Bytes(), &result))
	require.Equal(t, 7, result.Data.MaxSlowRequests)
	require.Equal(t, 180, result.Data.ThresholdSeconds)
	require.Equal(t, 1800, result.Data.WindowSeconds)

	zero := circuitSettingsRequest(router, http.MethodPut, `{"max_slow_requests":0}`)
	require.Equal(t, http.StatusOK, zero.Code)
	require.Equal(t, "1", repo.value)
}

func TestTotalDurationCircuitSettingsHandlerRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{`{}`, `{"max_slow_requests":null}`, `{"max_slow_requests":-1}`, `{"max_slow_requests":1.5}`, `{"max_slow_requests":10000}`, `{"max_slow_requests":"7"}`, `{`} {
		t.Run(body, func(t *testing.T) {
			repo := &totalDurationCircuitHandlerRepo{value: "16"}
			result := circuitSettingsRequest(totalDurationCircuitTestRouter(repo), http.MethodPut, body)
			require.Equal(t, http.StatusBadRequest, result.Code)
			require.Equal(t, "16", repo.value)
		})
	}
}

func TestTotalDurationCircuitSettingsHandlerReportsStorageFailures(t *testing.T) {
	repo := &totalDurationCircuitHandlerRepo{value: "16", setErr: errors.New("write failed")}
	router := totalDurationCircuitTestRouter(repo)
	require.Equal(t, http.StatusInternalServerError, circuitSettingsRequest(router, http.MethodPut, `{"max_slow_requests":7}`).Code)
	require.Equal(t, "16", repo.value)
	repo.getErr = errors.New("read failed")
	require.Equal(t, http.StatusInternalServerError, circuitSettingsRequest(router, http.MethodGet, "").Code)
}
