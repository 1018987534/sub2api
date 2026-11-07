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

type fastPoolThresholdHandlerRepo struct {
	service.SettingRepository
	value          string
	getErr, setErr error
}

func (r *fastPoolThresholdHandlerRepo) GetValue(_ context.Context, key string) (string, error) {
	if r.getErr != nil {
		return "", r.getErr
	}
	if key != service.SettingKeyFastPoolThresholds || r.value == "" {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}

func (r *fastPoolThresholdHandlerRepo) Set(_ context.Context, _ string, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	r.value = value
	return nil
}

func fastPoolThresholdRequest(repo *fastPoolThresholdHandlerRepo, method, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := NewSettingHandler(service.NewSettingService(repo, nil), nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/settings/fast-pool-thresholds", h.GetFastPoolThresholdSettings)
	router.PUT("/settings/fast-pool-thresholds", h.UpdateFastPoolThresholdSettings)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/settings/fast-pool-thresholds", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestFastPoolThresholdSettingsHandlerPersistsAndReadsBack(t *testing.T) {
	repo := &fastPoolThresholdHandlerRepo{}
	initial := fastPoolThresholdRequest(repo, http.MethodGet, "")
	require.Equal(t, http.StatusOK, initial.Code)
	require.Contains(t, initial.Body.String(), `"enter_seconds":30,"exit_seconds":33`)
	updated := fastPoolThresholdRequest(repo, http.MethodPut, `{"enter_seconds":28,"exit_seconds":31}`)
	require.Equal(t, http.StatusOK, updated.Code)
	readback := fastPoolThresholdRequest(repo, http.MethodGet, "")
	require.Equal(t, http.StatusOK, readback.Code)
	require.Contains(t, readback.Body.String(), `"enter_seconds":28,"exit_seconds":31`)
	for _, body := range []string{`{}`, `{"enter_seconds":null,"exit_seconds":33}`, `{"enter_seconds":0,"exit_seconds":33}`, `{"enter_seconds":34,"exit_seconds":33}`, `{"enter_seconds":28.5,"exit_seconds":33}`, `{"enter_seconds":"28","exit_seconds":33}`, `{"enter_seconds":28,"exit_seconds":86401}`, `{`} {
		rejected := fastPoolThresholdRequest(repo, http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, rejected.Code, body)
		require.Equal(t, `{"enter_seconds":28,"exit_seconds":31}`, repo.value)
	}
	repo.setErr = errors.New("write failed")
	require.Equal(t, http.StatusInternalServerError, fastPoolThresholdRequest(repo, http.MethodPut, `{"enter_seconds":30,"exit_seconds":33}`).Code)
	repo.getErr = errors.New("read failed")
	require.Equal(t, http.StatusInternalServerError, fastPoolThresholdRequest(repo, http.MethodGet, "").Code)
}
