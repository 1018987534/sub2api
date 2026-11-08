package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const instructionPolicyRejectionFixture = `{"error":{"type":"content_policy_violation","message":"请求附带的系统、开发者或用户配置指令命中已配置的危险指令片段，请检查客户端指令配置。"}}`

type requestRejectionFailoverUpstream struct {
	service.HTTPUpstream
	status     int
	body       string
	allFail    bool
	stream     bool
	accountIDs []int64
	bodies     [][]byte
}

func (u *requestRejectionFailoverUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.accountIDs = append(u.accountIDs, accountID)
	u.bodies = append(u.bodies, body)
	if accountID == 12440 || u.allFail {
		return &http.Response{StatusCode: u.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
	}
	const success = `{"id":"resp_recovered","object":"response","model":"gpt-6.1-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`
	if u.stream {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + success + "}\n\ndata: [DONE]\n\n"))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(success))}, nil
}

func TestOpenAIResponsesAccountRequestRejectionFailover(t *testing.T) {
	for _, tc := range []struct {
		name                string
		status              int
		body                string
		stream              bool
		allFail             bool
		fallbackPassthrough bool
	}{
		{"instruction_policy_to_native", http.StatusBadRequest, instructionPolicyRejectionFixture, false, false, false},
		{"instruction_policy_stream", http.StatusBadRequest, instructionPolicyRejectionFixture, true, false, true},
		{"missing_model_404", http.StatusNotFound, `{"error":{"type":"model_not_found","message":"Model is not supported by any configured account in this group"}}`, false, false, true},
		{"instruction_policy_all_reject", http.StatusBadRequest, instructionPolicyRejectionFixture, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			groupID := int64(79)
			accounts := []service.Account{
				{ID: 12440, Name: "rejecting-fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Priority: 0,
					Credentials: map[string]any{"api_key": "fixture-first", "base_url": "https://first.example.test"},
					Extra:       map[string]any{"openai_passthrough": true, "openai_responses_supported": true}},
				{ID: 12431, Name: "fallback-fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Priority: 1,
					Credentials: map[string]any{"api_key": "fixture-second", "base_url": "https://second.example.test"},
					Extra:       map[string]any{"openai_passthrough": tc.fallbackPassthrough, "openai_responses_supported": true}},
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Gateway.MaxAccountSwitches = 1
			repo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
			upstream := &requestRejectionFailoverUpstream{status: tc.status, body: tc.body, allFail: tc.allFail, stream: tc.stream}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			gateway := service.NewOpenAIGatewayService(
				repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, nil,
			)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			requestBody := `{"model":"gpt-6.1-sol","instructions":"Follow the project conventions.","input":"Summarize this repository.","stream":false}`
			if tc.stream {
				requestBody = strings.Replace(requestBody, `"stream":false`, `"stream":true`, 1)
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 129, GroupID: &groupID, User: &service.User{ID: 1, Status: service.StatusActive},
				Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})

			h.Responses(c)

			require.Equal(t, []int64{12440, 12431}, upstream.accountIDs, "the handler must select a different account exactly once")
			for _, body := range upstream.bodies {
				require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(body, "model").String())
				require.Equal(t, "Follow the project conventions.", gjson.GetBytes(body, "instructions").String())
				require.Equal(t, "Summarize this repository.", gjson.GetBytes(body, "input").String())
			}
			if tc.allFail {
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Equal(t, "content_policy_violation", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
				require.Contains(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String(), "请检查客户端指令配置")
				require.NotContains(t, rec.Body.String(), "completed")
			} else {
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "resp_recovered")
				require.NotContains(t, rec.Body.String(), "Upstream request failed")
			}
			for _, account := range repo.accounts {
				require.Equal(t, service.StatusActive, account.Status)
				require.True(t, account.Schedulable)
			}
			t.Logf("accounts=%v status=%d stream=%t all_reject=%t", upstream.accountIDs, rec.Code, tc.stream, tc.allFail)
		})
	}
}
