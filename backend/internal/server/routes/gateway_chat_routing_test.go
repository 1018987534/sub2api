package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayChatCompletionsSurface(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterResponsesGatewayRoutes(router, &handler.Handlers{
		Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{},
	}, middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	}), nil, nil, nil, &config.Config{})
	for _, path := range []string{"/v1/chat/completions", "/chat/completions"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		t.Logf("POST %s status=%d", path, w.Code)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
}

func TestGatewayChatCompletionsCapacityAndModelPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&channelMonitorRouteSettingRepoStub{values: map[string]string{
		service.SettingKeyGatewayRoutingSettings: `{"nodes":[{"id":"node-a","origin":"https://node-a.example","target_weight":100,"max_concurrency":1}]}`,
	}}, &config.Config{})
	for _, control := range []bool{false, true} {
		router := gin.New()
		h := &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}}
		group := allowlistGroup(service.PlatformOpenAI, true, "allowed-model")
		auth := middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := int64(1)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, Group: group})
			c.Next()
		})
		cfg := &config.Config{InstanceID: "node-a", Gateway: config.GatewayConfig{MaxBodySize: 1024}}
		if control {
			h.AsyncImage = handler.NewAsyncImageHandler(nil, nil)
			RegisterGatewayRoutes(router, h, auth, nil, nil, nil, settings, nil, cfg)
		} else {
			RegisterResponsesGatewayRoutes(router, h, auth, nil, settings, nil, cfg)
		}
		for _, path := range []string{"/v1/chat/completions", "/chat/completions"} {
			blocked := httptest.NewRecorder()
			router.ServeHTTP(blocked, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"blocked-model"}`)))
			require.Equal(t, http.StatusNotFound, blocked.Code)
			require.Contains(t, blocked.Body.String(), "not available for this group")
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"allowed-model"}`))
			request.Header.Set(gatewayNodeCapacityNonceHeader, "fixture-nonce")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)
			require.Equal(t, http.StatusServiceUnavailable, w.Code, "control=%v path=%s body=%s", control, path, w.Body.String())
			require.Equal(t, "node_capacity_unavailable", w.Header().Get("X-Sub2API-Ingress-Reject"))
			require.Equal(t, "fixture-nonce", w.Header().Get(gatewayNodeCapacityNonceHeader))
		}
	}
}
