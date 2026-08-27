package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayNodeCapacityAdmissionRejectsBeforeHandlerAndRecovers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	settings := service.NewSettingService(&channelMonitorRouteSettingRepoStub{
		values: map[string]string{
			service.SettingKeyGatewayRoutingSettings: `{"monitor_url":"https://check.example","traffic_protection_enabled":true,"health_protection_enabled":true,"traffic_threshold_percent":90,"overflow_node_id":"node-b","nodes":[{"id":"node-a","origin":"https://node-a.example","target_weight":100,"max_concurrency":1},{"id":"node-b","origin":"https://node-b.example","target_weight":0,"max_concurrency":2}]}`,
		},
	}, &config.Config{})
	settings.SetGatewayRoutingCapacityStore(service.NewGatewayNodeCapacityStore(rdb))

	entered := make(chan struct{})
	release := make(chan struct{})
	var handlerCalls atomic.Int32
	var handlerCapacityNonce atomic.Value
	var enteredOnce sync.Once
	router := gin.New()
	router.POST("/v1/responses", gatewayNodeCapacityAdmission(settings, &config.Config{InstanceID: "node-a"})(func(c *gin.Context) {
		handlerCalls.Add(1)
		handlerCapacityNonce.Store(c.GetHeader(gatewayNodeCapacityNonceHeader))
		enteredOnce.Do(func() { close(entered) })
		<-release
		c.Status(http.StatusOK)
	}))

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))
		firstDone <- recorder
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter the handler")
	}

	blocked := httptest.NewRecorder()
	blockedRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	blockedRequest.Header.Set(gatewayNodeCapacityNonceHeader, "edge-attempt-2")
	router.ServeHTTP(blocked, blockedRequest)
	require.Equal(t, http.StatusServiceUnavailable, blocked.Code)
	require.Equal(t, "node_capacity", blocked.Header().Get("X-Sub2API-Ingress-Reject"))
	require.Equal(t, "node-a", blocked.Header().Get("X-Sub2API-Node-ID"))
	require.Equal(t, "edge-attempt-2", blocked.Header().Get(gatewayNodeCapacityNonceHeader))
	require.EqualValues(t, 1, handlerCalls.Load())

	close(release)
	require.Equal(t, http.StatusOK, (<-firstDone).Code)

	recovered := httptest.NewRecorder()
	recoveredRelease := make(chan struct{})
	release = recoveredRelease
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(recoveredRelease)
	}()
	router.ServeHTTP(recovered, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusOK, recovered.Code)
	require.EqualValues(t, 2, handlerCalls.Load())
	require.Equal(t, "", handlerCapacityNonce.Load())
}

func TestGatewayNodeCapacityAdmissionStripsNonceWhenCapacityIsUnlimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&channelMonitorRouteSettingRepoStub{
		values: map[string]string{
			service.SettingKeyGatewayRoutingSettings: `{"nodes":[{"id":"node-a","origin":"https://node-a.example","target_weight":100,"max_concurrency":0}]}`,
		},
	}, &config.Config{})

	router := gin.New()
	router.POST("/v1/responses", gatewayNodeCapacityAdmission(settings, &config.Config{InstanceID: "node-a"})(func(c *gin.Context) {
		require.Empty(t, c.GetHeader(gatewayNodeCapacityNonceHeader))
		c.Status(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	request.Header.Set(gatewayNodeCapacityNonceHeader, "must-not-reach-handler")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}
