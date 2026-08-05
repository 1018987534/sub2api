package handler

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNextOpenAIWSSameAccountRetryIsBounded(t *testing.T) {
	account := &service.Account{ID: 41, Type: service.AccountTypeOAuth}
	err := &service.UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		RetryableOnSameAccount: true,
		RequestScopedTransient: true,
	}
	counts := make(map[int64]int)

	for i := 1; i <= 3; i++ {
		retry, count, limit := nextOpenAIWSSameAccountRetry(account, err, counts)
		require.True(t, retry)
		require.Equal(t, i, count)
		require.Equal(t, 3, limit)
	}
	retry, count, limit := nextOpenAIWSSameAccountRetry(account, err, counts)
	require.False(t, retry)
	require.Equal(t, 3, count)
	require.Equal(t, 3, limit)
	require.Equal(t, 3, counts[account.ID])
}

func TestNextOpenAIWSSameAccountRetryUsesAccountLimit(t *testing.T) {
	account := &service.Account{
		ID:   42,
		Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":             true,
			"pool_mode_retry_count": 1,
		},
	}
	err := &service.UpstreamFailoverError{RetryableOnSameAccount: true}
	counts := make(map[int64]int)

	retry, count, limit := nextOpenAIWSSameAccountRetry(account, err, counts)
	require.True(t, retry)
	require.Equal(t, 1, count)
	require.Equal(t, 1, limit)
	retry, count, limit = nextOpenAIWSSameAccountRetry(account, err, counts)
	require.False(t, retry)
	require.Equal(t, 1, count)
	require.Equal(t, 1, limit)
}
