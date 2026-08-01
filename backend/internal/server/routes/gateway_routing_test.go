package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayRoutingRuntimeStub struct {
	runtime *service.GatewayRoutingRuntime
}

func (s gatewayRoutingRuntimeStub) GetGatewayRoutingRuntime(context.Context) (*service.GatewayRoutingRuntime, error) {
	return s.runtime, nil
}

func TestGatewayRoutingRuntimeRouteRequiresDedicatedToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("disabled when token is not configured", func(t *testing.T) {
		router := gin.New()
		RegisterGatewayRoutingRuntimeRoutes(router.Group("/api/v1"), service.NewSettingService(nil, &config.Config{}), &config.Config{})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/gateway-routing/runtime", nil))
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})

	t.Run("rejects the wrong token before reading runtime state", func(t *testing.T) {
		router := gin.New()
		cfg := &config.Config{GatewayRoutingRuntimeToken: "expected-token"}
		RegisterGatewayRoutingRuntimeRoutes(router.Group("/api/v1"), nil, cfg)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/gateway-routing/runtime", nil)
		request.Header.Set(gatewayRoutingRuntimeTokenHeader, "wrong-token")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	t.Run("returns runtime for the correct token", func(t *testing.T) {
		router := gin.New()
		cfg := &config.Config{GatewayRoutingRuntimeToken: "expected-token"}
		provider := gatewayRoutingRuntimeStub{runtime: &service.GatewayRoutingRuntime{
			Nodes: []service.GatewayRoutingNodeRuntime{{
				ID:              "node-1",
				Origin:          "https://node-1.example",
				EffectiveWeight: 5,
			}},
		}}
		RegisterGatewayRoutingRuntimeRoutes(router.Group("/api/v1"), provider, cfg)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/gateway-routing/runtime", nil)
		request.Header.Set(gatewayRoutingRuntimeTokenHeader, "expected-token")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		require.Contains(t, recorder.Body.String(), `"effective_weight":5`)
	})
}
