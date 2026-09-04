package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesSameAccountRetryPolicy(t *testing.T) {
	tests := []struct {
		name          string
		account       *Account
		status        int
		shouldDisable bool
		retryable     bool
		retryMax      int
	}{
		{
			name:      "ordinary API key 429 retries five times",
			account:   &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			status:    http.StatusTooManyRequests,
			retryable: true,
			retryMax:  5,
		},
		{
			name:      "non-pool OAuth generic 502 retries five times",
			account:   &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
			status:    http.StatusBadGateway,
			retryable: true,
			retryMax:  5,
		},
		{
			name: "pool account keeps configured retry budget",
			account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"pool_mode": true,
			}},
			status:    http.StatusBadGateway,
			retryable: true,
			retryMax:  0,
		},
		{
			name:          "disabled account switches immediately",
			account:       &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			status:        http.StatusTooManyRequests,
			shouldDisable: true,
			retryable:     false,
			retryMax:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &UpstreamFailoverError{RetryableOnSameAccount: tt.name == "pool account keeps configured retry budget"}
			applyOpenAIResponsesSameAccountRetryPolicy(nil, tt.account, tt.status, tt.shouldDisable, err)
			require.Equal(t, tt.retryable, err.RetryableOnSameAccount)
			require.Equal(t, tt.retryMax, err.SameAccountRetryMax)
		})
	}
}

func TestDeferOpenAIAPIKey429AccountSideEffectsAfterFiveRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	for i := 0; i < 5; i++ {
		require.True(t, deferOpenAIAPIKey429AccountSideEffects(c, account, http.StatusTooManyRequests), "429 attempt %d should remain internal", i+1)
	}
	require.False(t, deferOpenAIAPIKey429AccountSideEffects(c, account, http.StatusTooManyRequests), "sixth 429 should trigger account side effects")
	require.True(t, deferOpenAIAPIKey429AccountSideEffects(c, &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, http.StatusTooManyRequests))
	require.False(t, deferOpenAIAPIKey429AccountSideEffects(c, &Account{ID: 13, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, http.StatusTooManyRequests))
}

func TestOpenAIStreamGenericFailureRetriesFiveBeforeAccountSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	payload := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","message":"Upstream request failed"}}}`)

	err := (&OpenAIGatewayService{}).newOpenAIStreamFailoverError(c, account, true, "req-1", payload, "Upstream request failed")

	require.Equal(t, http.StatusBadGateway, err.StatusCode)
	require.True(t, err.RetryableOnSameAccount)
	require.Equal(t, 5, err.SameAccountRetryMax)
}

func TestOpenAIResponsesSameAccountRetryPolicySkipsWrappedModelNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	err := &UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`{"error":{"code":"model_not_found","message":"unknown provider for model gpt-5.6-sol"}}`),
	}

	applyOpenAIResponsesSameAccountRetryPolicy(c, account, err.StatusCode, false, err)

	require.False(t, err.RetryableOnSameAccount)
	require.Zero(t, err.SameAccountRetryMax)
}
