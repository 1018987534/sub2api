package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const tempUnschedFailureCounterPrefix = "temp_unsched_failures:account:"

var tempUnschedFailureRecordScript = redis.NewScript(`
	local counter_key = KEYS[1]
	local registry_key = KEYS[2]
	local event_id = ARGV[1]
	local window_seconds = tonumber(ARGV[2])
	local now = redis.call('TIME')
	local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
	local cutoff_ms = now_ms - window_seconds * 1000
	local ttl_seconds = window_seconds + 60

	redis.call('ZREMRANGEBYSCORE', counter_key, '-inf', cutoff_ms)
	redis.call('ZADD', counter_key, 'NX', now_ms, event_id)
	redis.call('EXPIRE', counter_key, ttl_seconds)
	redis.call('SADD', registry_key, counter_key)
	local registry_ttl = redis.call('TTL', registry_key)
	if registry_ttl < ttl_seconds then
		redis.call('EXPIRE', registry_key, ttl_seconds)
	end
	return redis.call('ZCARD', counter_key)
`)

var tempUnschedFailureResetScript = redis.NewScript(`
	local registry_key = KEYS[1]
	local keys = redis.call('SMEMBERS', registry_key)
	for _, key in ipairs(keys) do
		redis.call('DEL', key)
	end
	redis.call('DEL', registry_key)
	return #keys
`)

type tempUnschedFailureCounterCache struct {
	rdb *redis.Client
}

func NewTempUnschedFailureCounterCache(rdb *redis.Client) service.TempUnschedFailureCounterCache {
	return &tempUnschedFailureCounterCache{rdb: rdb}
}

func (c *tempUnschedFailureCounterCache) RecordFailure(ctx context.Context, accountID int64, ruleKey string, windowSeconds int, eventID string) (int64, error) {
	if accountID <= 0 || strings.TrimSpace(ruleKey) == "" || windowSeconds <= 0 || strings.TrimSpace(eventID) == "" {
		return 0, nil
	}
	registryKey := fmt.Sprintf("%s%d:rules", tempUnschedFailureCounterPrefix, accountID)
	counterKey := fmt.Sprintf("%s%d:rule:%s", tempUnschedFailureCounterPrefix, accountID, ruleKey)
	count, err := tempUnschedFailureRecordScript.Run(ctx, c.rdb, []string{counterKey, registryKey}, eventID, windowSeconds).Int64()
	if err != nil {
		return 0, fmt.Errorf("record temp unsched failure: %w", err)
	}
	return count, nil
}

func (c *tempUnschedFailureCounterCache) ResetFailures(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return nil
	}
	registryKey := fmt.Sprintf("%s%d:rules", tempUnschedFailureCounterPrefix, accountID)
	if _, err := tempUnschedFailureResetScript.Run(ctx, c.rdb, []string{registryKey}).Result(); err != nil {
		return fmt.Errorf("reset temp unsched failures: %w", err)
	}
	return nil
}
