package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type failureRecordingLatencyCache struct {
	staticFirstTokenLatencyStatsCache
	accountID  int64
	requestID  string
	durationMS int
}

func (c *failureRecordingLatencyCache) RecordFailure(_ context.Context, accountID int64, requestID string, durationMS int) error {
	c.accountID = accountID
	c.requestID = requestID
	c.durationMS = durationMS
	return nil
}

func poolAPIKeyAccountForFailureTest() *Account {
	return &Account{
		ID:          12006,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"pool_mode": true},
	}
}

func TestOpenAIStreamFailureShouldRecordLatency(t *testing.T) {
	account := poolAPIKeyAccountForFailureTest()
	tests := []struct {
		name       string
		ctx        context.Context
		statusCode int
		payload    string
		message    string
		want       bool
	}{
		{
			name:       "semantic rate limit",
			statusCode: http.StatusTooManyRequests,
			payload:    `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"rate limited"}}}`,
			want:       true,
		},
		{
			name:       "upstream server failure",
			statusCode: http.StatusBadGateway,
			payload:    `{"type":"response.failed","response":{"error":{"code":"server_error","message":"upstream failed"}}}`,
			want:       true,
		},
		{
			name:       "capacity shed is request scoped",
			statusCode: http.StatusServiceUnavailable,
			payload:    `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"overloaded"}}}`,
			want:       false,
		},
		{
			name:       "cyber policy is request scoped",
			statusCode: http.StatusBadGateway,
			payload:    `{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"blocked"}}}`,
			want:       false,
		},
		{
			name:       "client cancellation is not account health",
			ctx:        canceledContext(),
			statusCode: http.StatusBadGateway,
			message:    "upstream stream disconnected",
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			require.Equal(t, tt.want, openAIStreamFailureShouldRecordLatency(ctx, account, tt.statusCode, []byte(tt.payload), tt.message))
		})
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestObserveTotalDurationFailureUsesHighSyntheticSample(t *testing.T) {
	previous := CurrentTotalDurationSettings()
	t.Cleanup(func() {
		setCurrentTotalDurationSettings(previous.FastThresholdSeconds, previous.SlowThresholdSeconds, previous.SampleLimit, previous.MinimumSamples, previous.PrimaryWindowHours, previous.SingleSampleCircuitSeconds)
	})
	setCurrentTotalDurationSettings(12, 16, 50, 20, 6, 60)

	cache := &failureRecordingLatencyCache{}
	svc := &RateLimitService{firstTokenLatencyStatsCache: cache}
	svc.ObserveTotalDurationFailure(context.Background(), poolAPIKeyAccountForFailureTest(), "upstream-request-1", 60_001)
	require.Equal(t, int64(12006), cache.accountID)
	require.Equal(t, "upstream-request-1", cache.requestID)
	require.Equal(t, 60_001, cache.durationMS)
}
