package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestCommonHealthRoutesReportRoleAndDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectPing()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	router := gin.New()
	RegisterCommonRoutes(router, &config.Config{InstanceRole: config.InstanceRoleGateway, InstanceID: "gateway-old"}, db, rdb)

	for _, path := range []string{"/health/live", "/health/ready"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"role":"gateway"`)
		require.Contains(t, w.Body.String(), `"instance_id":"gateway-old"`)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReadyReturnsServiceUnavailableWithoutSharedState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router, &config.Config{InstanceRole: config.InstanceRoleGateway}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), `"status":"not_ready"`)
	require.Contains(t, w.Body.String(), `"database":"unavailable"`)
	require.Contains(t, w.Body.String(), `"redis":"unavailable"`)
}

func TestGatewayCommonRoutesExcludeControlEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router, &config.Config{InstanceRole: config.InstanceRoleGateway}, nil, nil)

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	require.False(t, registered[http.MethodGet+" /setup/status"])
	require.False(t, registered[http.MethodPost+" /api/event_logging/batch"])
	require.True(t, registered[http.MethodGet+" /health/ready"])
}
