package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const accountInstructionRejectionBody = `{"error":{"message":"请求附带的系统、开发者或用户配置指令命中已配置的危险指令片段，请检查客户端指令配置。","type":"content_policy_violation"}}`

func TestOpenAIAccountRequestRejectionFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name         string
			status       int
			body         string
			wantFailover bool
		}{
			{"provider_instruction_policy", http.StatusBadRequest, accountInstructionRejectionBody, true},
			{"missing_model_400", http.StatusBadRequest, `{"error":{"code":"model_not_found","message":"unknown model"}}`, true},
			{"missing_model_404_type", http.StatusNotFound, `{"error":{"type":"model_not_found","message":"Model is not supported by any configured account in this group"}}`, true},
			{"invalid_schema", http.StatusBadRequest, openAIInvalidFunctionParametersBody, false},
			{"echoed_policy", http.StatusBadRequest, `{"error":{"type":"invalid_request_error","message":"Invalid input"},"echo":{"type":"content_policy_violation"}}`, false},
			{"cyber_policy", http.StatusBadRequest, `{"error":{"type":"content_policy_violation","code":"cyber_policy","message":"blocked"}}`, false},
			{"context_window", http.StatusBadRequest, `{"error":{"type":"content_policy_violation","code":"context_length_exceeded","message":"Your input exceeds the context window of this model."}}`, false},
		} {
			t.Run(fmt.Sprintf("%s/passthrough_%t", tc.name, passthrough), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				const input = `{"model":"gpt-6.1-sol","stream":false,"instructions":"Follow the project conventions.","input":"Summarize this repository."}`
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				body := &passthroughCloseTrackingReadCloser{Reader: strings.NewReader(tc.body)}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body,
				}}
				svc := &OpenAIGatewayService{
					cfg: &config.Config{}, httpUpstream: upstream, accountRepo: &modelNotFoundManagedAccountRepo{},
				}
				account := &Account{
					ID: 12440, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example.test"},
					Extra:       map[string]any{"openai_passthrough": passthrough, "openai_responses_supported": true},
				}

				result, err := svc.Forward(context.Background(), c, account, []byte(input))

				require.Error(t, err)
				require.Nil(t, result)
				require.True(t, body.closed)
				var failover *UpstreamFailoverError
				require.Equal(t, tc.wantFailover, errors.As(err, &failover), "error=%v response=%s", err, rec.Body.String())
				if tc.wantFailover {
					require.True(t, failover.ShouldRetryNextAccount())
					require.False(t, failover.RetryableOnSameAccount)
					require.False(t, c.Writer.Written(), "another account must be tried before committing an error")
					if passthrough {
						require.True(t, bytes.Equal([]byte(input), upstream.lastBody), "do not rewrite the client's instructions to obtain a retry")
					}
				} else {
					require.True(t, c.Writer.Written())
				}
				require.Equal(t, StatusActive, account.Status)
				require.True(t, account.Schedulable)
			})
		}
	}
}

func TestOpenAIAccountRequestRejectionPoolSkipsSameAccountRetry(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough_%t", passthrough), func(t *testing.T) {
			c, _ := newOpenAIUpstreamErrorTestContext(t)
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: &modelNotFoundManagedAccountRepo{}}
			account := &Account{
				ID: 12440, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example.test", "pool_mode": true, "pool_mode_retry_status_codes": []any{float64(http.StatusBadRequest)}},
				Extra:       map[string]any{"openai_passthrough": passthrough, "openai_responses_supported": true},
			}
			body := []byte(accountInstructionRejectionBody)
			svc.httpUpstream = &httpUpstreamRecorder{resp: newOpenAIUpstreamErrorResponse(http.StatusBadRequest, string(body))}
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6.1-sol","input":"hello","stream":false}`))
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.ShouldRetryNextAccount())
			require.False(t, failover.RetryableOnSameAccount, "a provider's instruction policy cannot be fixed by retrying that provider")
		})
	}
}
