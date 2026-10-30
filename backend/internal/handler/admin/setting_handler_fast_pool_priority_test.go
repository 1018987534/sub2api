package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func fastPoolPriorityTestRouter(repo *totalDurationCircuitHandlerRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewSettingHandler(service.NewSettingService(repo, nil), nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/settings/fast-pool-priority", h.GetFastPoolPrioritySettings)
	router.PUT("/settings/fast-pool-priority", h.UpdateFastPoolPrioritySettings)
	return router
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
	for _, mode := range []string{"rate", "intelligence"} {
		updated := fastPoolPriorityRequest(router, http.MethodPut, `{"mode":"`+mode+`"}`)
		require.Equal(t, http.StatusOK, updated.Code)
		require.Equal(t, mode, repo.value)
		readback := fastPoolPriorityRequest(fastPoolPriorityTestRouter(repo), http.MethodGet, "")
		require.Equal(t, http.StatusOK, readback.Code)
		require.Contains(t, readback.Body.String(), `"mode":"`+mode+`"`)
	}
}

func TestFastPoolPrioritySettingsHandlerRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{`{}`, `{"mode":null}`, `{"mode":""}`, `{"mode":"cache"}`, `{"mode":" rate"}`, `{"mode":1}`, `{`} {
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
