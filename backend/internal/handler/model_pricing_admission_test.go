//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelPricingAdmissionUpstream struct {
	service.HTTPUpstream
	calls []string
}

func (u *modelPricingAdmissionUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, req.URL.Path)
	body := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"cachedContentTokenCount":4}}`
	contentType := "application/json"
	if strings.Contains(req.URL.Path, "streamGenerateContent") {
		contentType = "text/event-stream"
		body = "data: " + body + "\n\n"
	}
	if strings.Contains(req.URL.Path, "countTokens") {
		body = `{"totalTokens":10}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Content-Type": []string{contentType}, "X-Request-Id": []string{"admission-test"},
	}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestGeminiPricingAdmissionBeforeUpstreamAndUsage(t *testing.T) {
	const model = "gemini-4-neon"
	const alias = "external-gemini-model"
	for _, route := range []struct {
		name, path, payload string
		streaming           bool
	}{
		{"gemini REST", "/v1beta/models/" + alias + ":generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, false},
		{"gemini SSE", "/v1beta/models/" + alias + ":streamGenerateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, true},
		{"chat SSE compatibility", "/v1/chat/completions", `{"model":"` + alias + `","stream":true,"messages":[{"role":"user","content":"hello"}]}`, true},
	} {
		for _, rates := range []string{"missing", "priced", "free"} {
			t.Run(route.name+"/"+rates, func(t *testing.T) {
				usage, upstream, router, group := newGeminiPricingAdmissionTest(t, alias, model)
				if rates != "missing" {
					input, output, cached := 3e-6, 15e-6, 0.15e-6
					if rates == "free" {
						input, output, cached = 0, 0, 0
					}
					group.ModelPricing = []service.ChannelModelPricing{{Models: []string{model}, InputPrice: &input, OutputPrice: &output, CacheReadPrice: &cached}}
				}
				record := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.payload))
				req.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(record, req)
				if rates == "missing" {
					require.Equal(t, http.StatusServiceUnavailable, record.Code, record.Body.String())
					require.Equal(t, 1, strings.Count(record.Body.String(), service.ModelPricingUnavailableMessage))
					require.Empty(t, upstream.calls)
					require.Empty(t, usage.totals())
					return
				}
				require.Equal(t, http.StatusOK, record.Code, record.Body.String())
				if route.streaming {
					require.Contains(t, record.Body.String(), "data:")
				}
				require.Len(t, upstream.calls, 1)
				require.Contains(t, upstream.calls[0], model)
				require.Len(t, usage.totals(), 1)
				if rates == "free" {
					require.Zero(t, usage.first().ActualCost)
				} else {
					require.InDelta(t, (6*3e-6+5*15e-6+4*0.15e-6)*0.59, usage.first().ActualCost, 1e-10)
				}
			})
		}
	}
}

func TestGeminiPricingAdmissionCountTokensExemption(t *testing.T) {
	const model = "unpriced-gemini-admission-test"
	usage, upstream, router, _ := newGeminiPricingAdmissionTest(t, model, model)
	record := httptest.NewRecorder()
	router.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/v1beta/models/"+model+":countTokens",
		strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)))
	require.Equal(t, http.StatusOK, record.Code, record.Body.String())
	require.Len(t, upstream.calls, 1)
	require.Equal(t, []float64{0}, usage.totals())
}

func newGeminiPricingAdmissionTest(t *testing.T, alias, model string) (*cachedBillingUsageRepo, *modelPricingAdmissionUpstream, *gin.Engine, *service.Group) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	group := &service.Group{ID: 118, Platform: service.PlatformGemini, Status: service.StatusActive, RateMultiplier: 0.59, Hydrated: true}
	account := &service.Account{ID: 24, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey, Status: service.StatusActive,
		Schedulable: true, Concurrency: 5, AccountGroups: []service.AccountGroup{{AccountID: 24, GroupID: group.ID}},
		Credentials: map[string]any{"api_key": "test-key", "model_mapping": map[string]any{alias: model}}}
	accountRepo := &cachedBillingAccountRepo{accounts: map[int64]*service.Account{account.ID: account}}
	groupRepo := &fakeGroupRepo{group: group}
	schedule := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, accountRepo, groupRepo, cfg)
	upstream := &modelPricingAdmissionUpstream{}
	userRepo := &cachedBillingUserRepo{balance: 1000}
	usage := &cachedBillingUsageRepo{}
	billing := service.NewBillingService(cfg, nil)
	billingCache := service.NewBillingCacheService(nil, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewGatewayService(accountRepo, groupRepo, usage, nil, userRepo, nil, nil, nil, cfg, schedule, nil,
		billing, nil, billingCache, nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
		service.NewModelPricingResolver(nil, billing), nil, nil, nil)
	compat := service.NewGeminiMessagesCompatService(accountRepo, groupRepo, nil, schedule, nil, nil, upstream, nil, cfg)
	h := &GatewayHandler{gatewayService: gateway, geminiCompatService: compat, billingCacheService: billingCache, cfg: cfg,
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatNone, 0)}
	key := &service.APIKey{ID: 7, UserID: 3, GroupID: &group.ID, Group: group, Status: service.StatusActive,
		User: &service.User{ID: 3, Concurrency: 10, Balance: 1000}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, group))
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 10})
		c.Next()
	})
	router.POST("/v1beta/models/*modelAction", h.GeminiV1BetaModels)
	router.POST("/v1/chat/completions", h.ChatCompletions)
	return usage, upstream, router, group
}
