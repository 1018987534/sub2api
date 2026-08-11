package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const firstTokenLatencyCounterPrefix = "first_token_latency:account:"

var firstTokenLatencyRecordScript = redis.NewScript(`
	local total_key = KEYS[1]
	local slow_key = KEYS[2]
	local registry_key = KEYS[3]
	local claim_key = KEYS[4]
	local event_id = ARGV[1]
	local window_seconds = tonumber(ARGV[2])
	local is_slow = tonumber(ARGV[3])
	if redis.call('EXISTS', claim_key) == 1 then
		return {-1, -1}
	end
	local now = redis.call('TIME')
	local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
	local cutoff_ms = now_ms - window_seconds * 1000
	local ttl_seconds = window_seconds + 60
	redis.call('ZREMRANGEBYSCORE', total_key, '-inf', cutoff_ms)
	redis.call('ZREMRANGEBYSCORE', slow_key, '-inf', cutoff_ms)
	redis.call('ZADD', total_key, 'NX', now_ms, event_id)
	if is_slow == 1 then
		redis.call('ZADD', slow_key, 'NX', now_ms, event_id)
	end
	redis.call('EXPIRE', total_key, ttl_seconds)
	redis.call('EXPIRE', slow_key, ttl_seconds)
	redis.call('SADD', registry_key, total_key, slow_key)
	local registry_ttl = redis.call('TTL', registry_key)
	if registry_ttl < ttl_seconds then
		redis.call('EXPIRE', registry_key, ttl_seconds)
	end
	return {redis.call('ZCARD', total_key), redis.call('ZCARD', slow_key)}
`)

var firstTokenLatencyResetScript = redis.NewScript(`
	local registry_key = KEYS[1]
	local keys = redis.call('SMEMBERS', registry_key)
	for _, key in ipairs(keys) do
		redis.call('DEL', key)
	end
	redis.call('DEL', registry_key)
	return #keys
`)

type firstTokenLatencyCounterCache struct {
	rdb *redis.Client
}

func NewFirstTokenLatencyCounterCache(rdb *redis.Client) service.FirstTokenLatencyCounterCache {
	return &firstTokenLatencyCounterCache{rdb: rdb}
}

func (c *firstTokenLatencyCounterCache) RecordFirstTokenSample(ctx context.Context, accountID int64, ruleKey string, windowSeconds int, eventID string, slow bool) (service.FirstTokenLatencySampleCounts, error) {
	ruleKey = strings.TrimSpace(ruleKey)
	eventID = strings.TrimSpace(eventID)
	if accountID <= 0 || ruleKey == "" || windowSeconds <= 0 || eventID == "" {
		return service.FirstTokenLatencySampleCounts{}, nil
	}
	registryKey := fmt.Sprintf("%s%d:rules", firstTokenLatencyCounterPrefix, accountID)
	totalKey := fmt.Sprintf("%s%d:rule:%s:total", firstTokenLatencyCounterPrefix, accountID, ruleKey)
	slowKey := fmt.Sprintf("%s%d:rule:%s:slow", firstTokenLatencyCounterPrefix, accountID, ruleKey)
	claimKey := fmt.Sprintf("%s%d:pause_claim", firstTokenLatencyCounterPrefix, accountID)
	isSlow := 0
	if slow {
		isSlow = 1
	}
	result, err := firstTokenLatencyRecordScript.Run(ctx, c.rdb, []string{totalKey, slowKey, registryKey, claimKey}, eventID, windowSeconds, isSlow).Slice()
	if err != nil {
		return service.FirstTokenLatencySampleCounts{}, fmt.Errorf("record first-token sample: %w", err)
	}
	if len(result) != 2 {
		return service.FirstTokenLatencySampleCounts{}, fmt.Errorf("record first-token sample: unexpected result length %d", len(result))
	}
	total, totalOK := result[0].(int64)
	slowCount, slowOK := result[1].(int64)
	if !totalOK || !slowOK {
		return service.FirstTokenLatencySampleCounts{}, fmt.Errorf("record first-token sample: unexpected result types")
	}
	return service.FirstTokenLatencySampleCounts{Total: total, Slow: slowCount}, nil
}

func (c *firstTokenLatencyCounterCache) ClaimFirstTokenPause(ctx context.Context, accountID int64, pauseSeconds int) (bool, error) {
	if accountID <= 0 || pauseSeconds <= 0 {
		return false, nil
	}
	claimKey := fmt.Sprintf("%s%d:pause_claim", firstTokenLatencyCounterPrefix, accountID)
	claimed, err := c.rdb.SetNX(ctx, claimKey, "1", time.Duration(pauseSeconds)*time.Second).Result()
	if err != nil {
		return false, fmt.Errorf("claim first-token pause: %w", err)
	}
	return claimed, nil
}

func (c *firstTokenLatencyCounterCache) ReleaseFirstTokenPauseClaim(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return nil
	}
	claimKey := fmt.Sprintf("%s%d:pause_claim", firstTokenLatencyCounterPrefix, accountID)
	if err := c.rdb.Del(ctx, claimKey).Err(); err != nil {
		return fmt.Errorf("release first-token pause claim: %w", err)
	}
	return nil
}

func (c *firstTokenLatencyCounterCache) ResetFirstTokenSamples(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return nil
	}
	registryKey := fmt.Sprintf("%s%d:rules", firstTokenLatencyCounterPrefix, accountID)
	if _, err := firstTokenLatencyResetScript.Run(ctx, c.rdb, []string{registryKey}).Result(); err != nil {
		return fmt.Errorf("reset first-token samples: %w", err)
	}
	return nil
}
