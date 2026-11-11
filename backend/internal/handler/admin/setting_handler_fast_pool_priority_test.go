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

type fastPoolPriorityHandlerRepo struct {
	*totalDurationCircuitHandlerRepo
}

func (r fastPoolPriorityHandlerRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	values := make(map[string]string)
	for _, key := range keys {
		if value, err := r.GetValue(ctx, key); err == nil {
			values[key] = value
		}
	}
	return values, nil
}

func (r fastPoolPriorityHandlerRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	if err := r.totalDurationCircuitHandlerRepo.SetMultiple(ctx, values); err != nil {
		return err
	}
	r.value = values[service.SettingKeyFastPoolPriorityMode]
	return nil
}

func fastPoolPriorityTestRouter(repo *totalDurationCircuitHandlerRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewSettingHandler(service.NewSettingService(fastPoolPriorityHandlerRepo{repo}, nil), nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/settings/fast-pool-priority", h.GetFastPoolPrioritySettings)
	router.PUT("/settings/fast-pool-priority", h.UpdateFastPoolPrioritySettings)
	return router
}

func TestFastPoolPrioritySettingsHandlerSavesAndClearsThreshold(t *testing.T) {
	repo := &totalDurationCircuitHandlerRepo{}
	for _, threshold := range []string{"80", "80.25", "0", "100", "null"} {
		body := `{"mode":"rate","cache_rate_threshold_percent":` + threshold + `}`
		updated := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodPut, body)
		require.Equal(t, http.StatusOK, updated.Code)
		readback := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodGet, "")
		require.Equal(t, http.StatusOK, readback.Code)
		require.Contains(t, readback.Body.String(), `"cache_rate_threshold_percent":`+threshold)
	}
	require.Empty(t, repo.values[service.SettingKeyFastPoolPriorityCacheRateThresholdPercent])
}

func TestFastPoolPrioritySettingsHandlerModeOnlyPreservesThreshold(t *testing.T) {
	for _, stored := range []string{"", "80.25"} {
		repo := &totalDurationCircuitHandlerRepo{values: map[string]string{
			service.SettingKeyFastPoolPriorityMode:                      "rate",
			service.SettingKeyFastPoolPriorityCacheRateThresholdPercent: stored,
		}}
		updated := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodPut, `{"mode":"latency"}`)
		require.Equal(t, http.StatusOK, updated.Code)
		require.Equal(t, stored, repo.values[service.SettingKeyFastPoolPriorityCacheRateThresholdPercent])
	}
}

func fastPoolPriorityRequest(router *gin.Engine, method, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/settings/fast-pool-priority", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestFastPoolPrioritySettingsHandlerPersistsAndReadsBack(t *testing.T) {
	repo := &totalDurationCircuitHandlerRepo{}
	router := fastPoolPriorityTestRouter(repo)
	initial := fastPoolPriorityRequest(router, http.MethodGet, "")
	require.Equal(t, http.StatusOK, initial.Code)
	require.Contains(t, initial.Body.String(), `"mode":"intelligence"`)
	require.Contains(t, initial.Body.String(), `"below_threshold_mode":"intelligence"`)
	require.Contains(t, initial.Body.String(), `"cache_rate_threshold_percent":80`)
	for _, mode := range []string{"rate", "latency", "intelligence"} {
		updated := fastPoolPriorityRequest(router, http.MethodPut, `{"mode":"`+mode+`"}`)
		require.Equal(t, http.StatusOK, updated.Code)
		require.Equal(t, mode, repo.value)
		readback := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodGet, "")
		require.Equal(t, http.StatusOK, readback.Code)
		require.Contains(t, readback.Body.String(), `"mode":"`+mode+`"`)
	}
}

func TestFastPoolPrioritySettingsHandlerRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{`{}`, `{"mode":null}`, `{"mode":""}`, `{"mode":"cache"}`, `{"mode":" rate"}`, `{"mode":1}`, `{`, `{"mode":"rate","cache_rate_threshold_percent":-1}`, `{"mode":"rate","cache_rate_threshold_percent":101}`, `{"mode":"rate","cache_rate_threshold_percent":"80"}`} {
		t.Run(body, func(t *testing.T) {
			repo := &totalDurationCircuitHandlerRepo{value: "intelligence"}
			result := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodPut, body)
			require.Equal(t, http.StatusBadRequest, result.Code)
			require.Equal(t, "intelligence", repo.value)
		})
	}
}

func TestFastPoolPrioritySettingsHandlerReportsStorageFailures(t *testing.T) {
	repo := &totalDurationCircuitHandlerRepo{value: "intelligence", setErr: errors.New("write failed")}
	router := fastPoolPriorityTestRouter(repo)
	require.Equal(t, http.StatusInternalServerError, fastPoolPriorityRequest(router, http.MethodPut, `{"mode":"rate"}`).Code)
	require.Equal(t, "intelligence", repo.value)
	repo.getErr = errors.New("read failed")
	require.Equal(t, http.StatusInternalServerError, fastPoolPriorityRequest(router, http.MethodGet, "").Code)
}

func TestFastPoolPrioritySettingsHandlerIndependentBelowThresholdMode(t *testing.T) {
	repo := &totalDurationCircuitHandlerRepo{}
	for _, mode := range []string{"rate", "latency", "intelligence"} {
		body := `{"mode":"rate","cache_rate_threshold_percent":80,"below_threshold_mode":"` + mode + `"}`
		updated := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodPut, body)
		require.Equal(t, http.StatusOK, updated.Code)
		readback := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodGet, "")
		require.Contains(t, readback.Body.String(), `"below_threshold_mode":"`+mode+`"`)
		// A pre-upgrade client must preserve the independently saved strategy.
		legacy := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodPut, `{"mode":"latency","cache_rate_threshold_percent":90}`)
		require.Equal(t, http.StatusOK, legacy.Code)
		require.Equal(t, mode, repo.values[service.SettingKeyFastPoolPriorityBelowThresholdMode])
	}
	for _, value := range []string{`"cache"`, `""`, `1`, `" rate"`} {
		result := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodPut, `{"mode":"rate","below_threshold_mode":`+value+`}`)
		require.Equal(t, http.StatusBadRequest, result.Code)
	}
}
