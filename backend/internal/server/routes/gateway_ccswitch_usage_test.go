package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ccSwitchUsageKeyRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *ccSwitchUsageKeyRepo) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	if key != r.key.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.key
	return &clone, nil
}

func (r *ccSwitchUsageKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

type ccSwitchUsageUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *ccSwitchUsageUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	clone := *r.user
	return &clone, nil
}

func (r *ccSwitchUsageUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func newCCSwitchUsageRouter(key *service.APIKey) *gin.Engine {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keyService := service.NewAPIKeyService(&ccSwitchUsageKeyRepo{key: key}, nil, nil, nil, nil, nil, cfg)
	userService := service.NewUserService(&ccSwitchUsageUserRepo{user: key.User}, nil, nil, nil)
	gatewayHandler := handler.NewGatewayHandler(
		nil, nil, nil, nil, userService, nil, nil, nil, keyService, nil, nil, nil, nil, cfg, nil,
	)
	router := gin.New()
	RegisterGatewayRoutes(router, &handler.Handlers{
		Gateway: gatewayHandler, OpenAIGateway: &handler.OpenAIGatewayHandler{},
	}, servermiddleware.NewAPIKeyAuthMiddleware(keyService, nil, cfg), keyService, nil, nil, nil, nil, cfg)
	return router
}

func ccSwitchUsageKey() *service.APIKey {
	groupID := int64(42)
	return &service.APIKey{
		ID: 100, UserID: 7, Key: "ccswitch-usage-test", Status: service.StatusActive,
		User:    &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 12.5},
		GroupID: &groupID,
		Group: &service.Group{
			ID: groupID, Status: service.StatusActive, Hydrated: true,
			Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard,
		},
	}
}

func TestGatewayRoutesCCSwitchUsagePaths(t *testing.T) {
	key := ccSwitchUsageKey()
	router := newCCSwitchUsageRouter(key)
	for _, path := range []string{"/v1/usage", "/v1/v1/usage"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+"?days=1", nil)
			req.Header.Set("Authorization", "Bearer "+key.Key)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			t.Logf("GET %s?days=1 HTTP=%d body=%s", path, w.Code, w.Body.String())
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Header().Get("Content-Type"), "application/json")
			require.JSONEq(t, `{"mode":"unrestricted","isValid":true,"planName":"钱包余额","remaining":12.5,"unit":"USD","balance":12.5}`, w.Body.String())
		})
	}
}

func TestGatewayRoutesCCSwitchUsageAuthentication(t *testing.T) {
	for _, path := range []string{"/v1/usage", "/v1/v1/usage"} {
		t.Run(path, func(t *testing.T) {
			for _, state := range []string{"missing", "invalid", "disabled", "inactive user", "inactive group", "expired", "exhausted", "zero balance"} {
				t.Run(state, func(t *testing.T) {
					key := ccSwitchUsageKey()
					credential := key.Key
					status := http.StatusOK
					switch state {
					case "missing":
						credential, status = "", http.StatusUnauthorized
					case "invalid":
						credential, status = "invalid", http.StatusUnauthorized
					case "disabled":
						key.Status, status = service.StatusDisabled, http.StatusUnauthorized
					case "inactive user":
						key.User.Status, status = service.StatusDisabled, http.StatusUnauthorized
					case "inactive group":
						key.Group.Status, status = service.StatusDisabled, http.StatusForbidden
					case "expired":
						past := time.Now().Add(-time.Hour)
						key.Status, key.ExpiresAt = service.StatusAPIKeyExpired, &past
					case "exhausted":
						key.Status, key.Quota, key.QuotaUsed = service.StatusAPIKeyQuotaExhausted, 1, 1
					case "zero balance":
						key.User.Balance = 0
					}
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if credential != "" {
						req.Header.Set("Authorization", "Bearer "+credential)
					}
					w := httptest.NewRecorder()
					newCCSwitchUsageRouter(key).ServeHTTP(w, req)
					require.Equal(t, status, w.Code, w.Body.String())
					if state == "exhausted" || state == "zero balance" {
						var body struct {
							Remaining float64 `json:"remaining"`
						}
						require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
						require.Zero(t, body.Remaining)
					}
				})
			}
		})
	}
}

func TestGatewayRoutesCCSwitchUsageAliasIsReadOnly(t *testing.T) {
	key := ccSwitchUsageKey()
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/v1/v1/usage"},
		{http.MethodGet, "/v1/v1/v1/usage"},
		{http.MethodGet, "/v1/v1/models"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(request.method, request.path, nil)
		req.Header.Set("Authorization", "Bearer "+key.Key)
		newCCSwitchUsageRouter(key).ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code)
	}
}
