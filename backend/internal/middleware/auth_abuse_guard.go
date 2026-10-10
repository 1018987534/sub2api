package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	authLoginFailureLimit  = 10
	authLoginFailureWindow = 15 * time.Minute
)

// AuthAbuseGuard shares budgets across application instances. Call email
// reservation only after captcha validation, so rejected bots cannot exhaust
// the global allowance without solving a challenge.
type AuthAbuseGuard struct {
	redis          *redis.Client
	normalizeEmail func(string) string
}

func NewAuthAbuseGuard(client *redis.Client, normalizeEmail func(string) string) *AuthAbuseGuard {
	if normalizeEmail == nil {
		normalizeEmail = func(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
	}
	return &AuthAbuseGuard{redis: client, normalizeEmail: normalizeEmail}
}

// Check every bucket before reserving any of them. A denied request neither
// spends the global budget nor extends a victim's cooldown.
var authEmailBudgetScript = redis.NewScript(`
for i,key in ipairs(KEYS) do
  local limit = tonumber(ARGV[(i-1)*2+1])
  local window = tonumber(ARGV[(i-1)*2+2])
  local current = tonumber(redis.call('GET', key) or '0')
  if current >= limit then
    local ttl = redis.call('PTTL', key)
    if ttl < 1 then
      redis.call('PEXPIRE', key, window)
      ttl = window
    end
    return {0, ttl}
  end
end
for i,key in ipairs(KEYS) do
  local count = redis.call('INCR', key)
  if count == 1 or redis.call('PTTL', key) < 0 then
    redis.call('PEXPIRE', key, tonumber(ARGV[(i-1)*2+2]))
  end
end
return {1, 0}
`)

func authIdentityHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func authIPBucket(address string) string {
	parsed := net.ParseIP(address)
	if parsed == nil || parsed.To4() != nil {
		return address
	}
	// IPv6 privacy addresses from the same network share one allowance.
	return (&net.IPNet{IP: parsed.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

func (g *AuthAbuseGuard) ReserveEmail(ctx context.Context, email, clientIP string) (AllowResult, error) {
	if g == nil || g.redis == nil {
		return AllowResult{}, fmt.Errorf("auth abuse guard unavailable")
	}
	mailbox := authIdentityHash(g.normalizeEmail(email))
	address := authIdentityHash(authIPBucket(clientIP))
	keys := []string{
		"auth_abuse:mail:mailbox:" + mailbox + ":minute",
		"auth_abuse:mail:mailbox:" + mailbox + ":hour",
		"auth_abuse:mail:mailbox:" + mailbox + ":day",
		"auth_abuse:mail:ip:" + address + ":ten_minutes",
		"auth_abuse:mail:ip:" + address + ":hour",
		"auth_abuse:mail:global:hour",
		"auth_abuse:mail:global:day",
	}
	args := []any{1, time.Minute.Milliseconds(), 5, time.Hour.Milliseconds(),
		10, (24 * time.Hour).Milliseconds(), 10, (10 * time.Minute).Milliseconds(),
		30, time.Hour.Milliseconds(), 100, time.Hour.Milliseconds(), 500, (24 * time.Hour).Milliseconds()}
	values, err := authEmailBudgetScript.Run(ctx, g.redis, keys, args...).Int64Slice()
	if err != nil {
		return AllowResult{}, err
	}
	if len(values) != 2 {
		return AllowResult{}, fmt.Errorf("unexpected auth email budget result")
	}
	return AllowResult{Allowed: values[0] == 1, RetryAfter: time.Duration(values[1]) * time.Millisecond}, nil
}

func (g *AuthAbuseGuard) loginKey(email string) string {
	return "auth_abuse:login:failures:" + authIdentityHash(g.normalizeEmail(email))
}

func (g *AuthAbuseGuard) CheckLogin(ctx context.Context, email string) (AllowResult, error) {
	if g == nil || g.redis == nil {
		return AllowResult{}, fmt.Errorf("auth abuse guard unavailable")
	}
	key := g.loginKey(email)
	count, err := g.redis.Get(ctx, key).Int64()
	if err != nil && err != redis.Nil {
		return AllowResult{}, err
	}
	result := AllowResult{Allowed: count < authLoginFailureLimit, Count: count}
	if !result.Allowed {
		result.RetryAfter = authLoginFailureWindow
		if ttl, err := g.redis.PTTL(ctx, key).Result(); err == nil && ttl > 0 {
			result.RetryAfter = ttl
		}
	}
	return result, nil
}

func (g *AuthAbuseGuard) RecordLoginFailure(ctx context.Context, email string) error {
	_, _, err := rateLimitRun(ctx, g.redis, g.loginKey(email), authLoginFailureWindow.Milliseconds())
	return err
}

func (g *AuthAbuseGuard) ClearLoginFailures(ctx context.Context, email string) error {
	return g.redis.Del(ctx, g.loginKey(email)).Err()
}
