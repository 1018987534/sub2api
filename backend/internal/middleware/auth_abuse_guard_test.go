package middleware

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func authGuardTest(t *testing.T) (*AuthAbuseGuard, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewAuthAbuseGuard(client, nil), mr
}

func TestAuthEmailBudgetConcurrentSameMailbox(t *testing.T) {
	guard, mr := authGuardTest(t)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := guard.ReserveEmail(context.Background(), " Person@Example.com ", "198.51.100.2")
			if err != nil {
				t.Error(err)
				return
			}
			if result.Allowed {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int64(1), accepted.Load())
	count, err := mr.Get("auth_abuse:mail:global:hour")
	require.NoError(t, err)
	require.Equal(t, "1", count, "denials must not consume the global allowance")
	for _, key := range mr.Keys() {
		require.NotContains(t, key, "person@")
		require.NotContains(t, key, "198.51.100.2")
	}
}

func TestAuthEmailBudgetCooldownDoesNotExtendOnDenial(t *testing.T) {
	guard, mr := authGuardTest(t)
	result, err := guard.ReserveEmail(context.Background(), "person@example.com", "198.51.100.2")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	mr.FastForward(30 * time.Second)
	result, err = guard.ReserveEmail(context.Background(), "person@example.com", "198.51.100.3")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, 30*time.Second, result.RetryAfter)
	mr.FastForward(31 * time.Second)
	result, err = guard.ReserveEmail(context.Background(), "person@example.com", "198.51.100.3")
	require.NoError(t, err)
	require.True(t, result.Allowed)
}

func TestAuthEmailBudgetSharesIPv6NetworkAndLimitsLongWindow(t *testing.T) {
	guard, _ := authGuardTest(t)
	for i := 0; i < 10; i++ {
		result, err := guard.ReserveEmail(context.Background(), fmt.Sprintf("person%d@example.com", i), fmt.Sprintf("2001:db8:1:2::%x", i+1))
		require.NoError(t, err)
		require.True(t, result.Allowed)
	}
	result, err := guard.ReserveEmail(context.Background(), "another@example.com", "2001:db8:1:2::ffff")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	result, err = guard.ReserveEmail(context.Background(), "another@example.com", "2001:db8:1:3::1")
	require.NoError(t, err)
	require.True(t, result.Allowed)
}

func TestAuthEmailBudgetGlobalLimitsChangingMailboxesAndIPs(t *testing.T) {
	guard, _ := authGuardTest(t)
	for i := 0; i < 100; i++ {
		result, err := guard.ReserveEmail(context.Background(), fmt.Sprintf("person%d@example.com", i), fmt.Sprintf("198.51.100.%d", i+1))
		require.NoError(t, err)
		require.True(t, result.Allowed)
	}
	result, err := guard.ReserveEmail(context.Background(), "new@example.com", "203.0.113.1")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, time.Hour, result.RetryAfter)
}

func TestAuthEmailBudgetUsesCanonicalMailbox(t *testing.T) {
	guard, _ := authGuardTest(t)
	guard.normalizeEmail = func(email string) string { return strings.Split(strings.ToLower(email), "+")[0] }
	result, err := guard.ReserveEmail(context.Background(), "person+first@example.com", "198.51.100.1")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	result, err = guard.ReserveEmail(context.Background(), "person+second@example.com", "198.51.100.2")
	require.NoError(t, err)
	require.False(t, result.Allowed)
}

func TestAuthLoginFailureCooldownAndSuccessfulReset(t *testing.T) {
	guard, mr := authGuardTest(t)
	for i := 0; i < authLoginFailureLimit; i++ {
		result, err := guard.CheckLogin(context.Background(), "person@example.com")
		require.NoError(t, err)
		require.True(t, result.Allowed)
		require.NoError(t, guard.RecordLoginFailure(context.Background(), "person@example.com"))
	}
	result, err := guard.CheckLogin(context.Background(), "PERSON@example.com")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, 15*time.Minute, result.RetryAfter)
	mr.FastForward(15 * time.Minute)
	result, err = guard.CheckLogin(context.Background(), "person@example.com")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	require.NoError(t, guard.RecordLoginFailure(context.Background(), "person@example.com"))
	require.NoError(t, guard.ClearLoginFailures(context.Background(), "person@example.com"))
	result, err = guard.CheckLogin(context.Background(), "person@example.com")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	require.Zero(t, result.Count)
}

func TestAuthAbuseGuardRedisFailureDoesNotAllowRequests(t *testing.T) {
	guard, mr := authGuardTest(t)
	mr.Close()
	result, err := guard.ReserveEmail(context.Background(), "person@example.com", "198.51.100.1")
	require.Error(t, err)
	require.False(t, result.Allowed)
	result, err = guard.CheckLogin(context.Background(), "person@example.com")
	require.Error(t, err)
	require.False(t, result.Allowed)
}
