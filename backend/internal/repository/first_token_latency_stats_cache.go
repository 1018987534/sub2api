package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const firstTokenLatencyStatsPrefix = "scheduler:first_token:account:"
const firstTokenLatencyProbePrefix = "scheduler:first_token:probe:"

// The recent median protects the prediction from one-off tail latency. The
// EWMA still moves on every observation and gives a recovered upstream more
// influence when the previous observation is old.
var firstTokenLatencyStatsRecordScript = redis.NewScript(`
	local stats_key = KEYS[1]
	local dedupe_key = KEYS[2]
	local probe_key = KEYS[3]
	local latency_ms = tonumber(ARGV[1])
	local stats_ttl_seconds = tonumber(ARGV[2])
	local dedupe_ttl_seconds = tonumber(ARGV[3])
	local now = redis.call('TIME')
	local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

	if redis.call('SET', dedupe_key, '1', 'NX', 'EX', dedupe_ttl_seconds) == false then
		return 0
	end

	local old_ewma = tonumber(redis.call('HGET', stats_key, 'ewma_ms'))
	local old_median = tonumber(redis.call('HGET', stats_key, 'median_ms'))
	local old_reliable_fast = tonumber(redis.call('HGET', stats_key, 'reliable_fast')) or 0
	local old_updated = tonumber(redis.call('HGET', stats_key, 'updated_at_ms'))
	local elapsed_seconds = 0
	if old_updated and now_ms > old_updated then
		elapsed_seconds = (now_ms - old_updated) / 1000
	end

	local fast_recovery = latency_ms <= 10000 and (
		not old_ewma or
		not old_median or
		elapsed_seconds > 1200 or
		(0.30 * old_median + 0.70 * old_ewma) > 10000)
	local samples = {}
	local encoded = redis.call('HGET', stats_key, 'recent_samples')
	if encoded and elapsed_seconds <= 1200 and not fast_recovery then
		for value in string.gmatch(encoded, '[^,]+') do
			table.insert(samples, tonumber(value))
		end
	end
	table.insert(samples, latency_ms)
	while #samples > 9 do
		table.remove(samples, 1)
	end

	local sorted = {}
	for i, value in ipairs(samples) do sorted[i] = value end
	table.sort(sorted)
	local median = 0
	if #sorted % 2 == 0 then
		median = (sorted[#sorted / 2] + sorted[#sorted / 2 + 1]) / 2
	else
		median = sorted[math.floor((#sorted + 1) / 2)]
	end

	local ewma = latency_ms
	if old_ewma and not fast_recovery then
		local alpha = 0.25
		if elapsed_seconds > 0 then
			local time_alpha = 1 - math.exp(-elapsed_seconds / 900)
			if time_alpha > alpha then alpha = time_alpha end
		end
		ewma = alpha * latency_ms + (1 - alpha) * old_ewma
	end

	local sample_count = tonumber(redis.call('HGET', stats_key, 'sample_count')) or 0
	if elapsed_seconds > 1200 then sample_count = 0 end
	sample_count = sample_count + 1
	-- A newly discovered or recovered sub-10-second account is immediately
	-- trustworthy enough to cross the hard scheduling boundary without inflating
	-- the real sample count shown to operators.
	local reliable_fast = 0
	if latency_ms <= 10000 and (fast_recovery or (elapsed_seconds <= 1200 and old_reliable_fast == 1)) then
		reliable_fast = 1
	end
	local slow_streak = tonumber(redis.call('HGET', stats_key, 'slow_streak')) or 0
	if elapsed_seconds > 1200 then
		slow_streak = 0
	elseif old_ewma and latency_ms >= old_ewma * 0.8 then
		slow_streak = slow_streak + 1
	else
		slow_streak = 0
	end
	local parts = {}
	for i, value in ipairs(samples) do parts[i] = tostring(value) end
	redis.call('HSET', stats_key,
		'ewma_ms', tostring(ewma),
		'median_ms', tostring(median),
		'sample_count', tostring(sample_count),
		'reliable_fast', tostring(reliable_fast),
		'slow_streak', tostring(slow_streak),
		'updated_at_ms', tostring(now_ms),
		'recent_samples', table.concat(parts, ','))
	redis.call('EXPIRE', stats_key, stats_ttl_seconds)
	redis.call('DEL', probe_key)
	return 1
`)

type firstTokenLatencyStatsCache struct {
	rdb *redis.Client
}

func NewFirstTokenLatencyStatsCache(rdb *redis.Client) service.FirstTokenLatencyStatsCache {
	return &firstTokenLatencyStatsCache{rdb: rdb}
}

func (c *firstTokenLatencyStatsCache) RecordSample(ctx context.Context, accountID int64, requestID string, firstTokenMs int) error {
	requestID = strings.TrimSpace(requestID)
	if accountID <= 0 || requestID == "" || firstTokenMs <= 0 {
		return nil
	}
	const statsTTL = 24 * time.Hour
	const dedupeTTL = 2 * time.Hour
	statsKey := fmt.Sprintf("%s%d", firstTokenLatencyStatsPrefix, accountID)
	dedupeKey := fmt.Sprintf("scheduler:first_token:event:%d:%s", accountID, requestID)
	probeKey := fmt.Sprintf("%s%d", firstTokenLatencyProbePrefix, accountID)
	if _, err := firstTokenLatencyStatsRecordScript.Run(ctx, c.rdb, []string{statsKey, dedupeKey, probeKey}, firstTokenMs, int(statsTTL.Seconds()), int(dedupeTTL.Seconds())).Result(); err != nil {
		return fmt.Errorf("record first-token latency stats: %w", err)
	}
	return nil
}

func (c *firstTokenLatencyStatsCache) TryClaimProbe(ctx context.Context, accountID int64, lease time.Duration) (bool, error) {
	if accountID <= 0 || lease <= 0 {
		return false, nil
	}
	claimed, err := c.rdb.SetNX(ctx, fmt.Sprintf("%s%d", firstTokenLatencyProbePrefix, accountID), "1", lease).Result()
	if err != nil {
		return false, fmt.Errorf("claim first-token probe: %w", err)
	}
	return claimed, nil
}

func (c *firstTokenLatencyStatsCache) GetStatsBatch(ctx context.Context, accountIDs []int64) (map[int64]service.FirstTokenLatencyStats, error) {
	result := make(map[int64]service.FirstTokenLatencyStats, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	pipe := c.rdb.Pipeline()
	commands := make(map[int64]*redis.SliceCmd, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		commands[accountID] = pipe.HMGet(ctx, fmt.Sprintf("%s%d", firstTokenLatencyStatsPrefix, accountID), "ewma_ms", "median_ms", "sample_count", "updated_at_ms", "slow_streak", "reliable_fast")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get first-token latency stats: %w", err)
	}
	for accountID, cmd := range commands {
		values, err := cmd.Result()
		if err != nil || len(values) != 6 || values[0] == nil {
			continue
		}
		ewma, ewmaErr := strconv.ParseFloat(fmt.Sprint(values[0]), 64)
		median, medianErr := strconv.ParseFloat(fmt.Sprint(values[1]), 64)
		count, countErr := strconv.ParseInt(fmt.Sprint(values[2]), 10, 64)
		updatedAtMS, updatedErr := strconv.ParseInt(fmt.Sprint(values[3]), 10, 64)
		slowStreak, streakErr := strconv.Atoi(fmt.Sprint(values[4]))
		reliableFast := values[5] != nil && fmt.Sprint(values[5]) == "1"
		if ewmaErr != nil || medianErr != nil || countErr != nil || updatedErr != nil || streakErr != nil || count <= 0 || updatedAtMS <= 0 {
			continue
		}
		result[accountID] = service.FirstTokenLatencyStats{
			PredictedMS:  0.30*median + 0.70*ewma,
			SampleCount:  count,
			UpdatedAt:    time.UnixMilli(updatedAtMS),
			SlowStreak:   slowStreak,
			ReliableFast: reliableFast,
		}
	}
	return result, nil
}
