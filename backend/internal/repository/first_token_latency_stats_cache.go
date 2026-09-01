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

const totalLatencyStatsPrefix = "scheduler:total_duration:account:"
const totalLatencySamplesPrefix = "scheduler:total_duration:samples:"
const totalLatencyProbePrefix = "scheduler:total_duration:probe:"
const totalLatencyManualProbePrefix = "scheduler:total_duration:manual_probe:"

// Each completed, billable stream updates one timestamped 24-hour window and
// atomically derives the scheduling score. The score is the 10%-90% trimmed
// mean of the latest six hours, falling back to 24 hours until six hours has
// enough observations. Pool transitions require three consecutive qualifying
// aggregate results, so one long but valid generation cannot flip an account.
var totalLatencyStatsRecordScript = redis.NewScript(`
	local stats_key = KEYS[1]
	local samples_key = KEYS[2]
	local dedupe_key = KEYS[3]
	local probe_key = KEYS[4]
	local manual_probe_key = KEYS[5]
	local duration_ms = tonumber(ARGV[1])
	local stats_ttl_seconds = tonumber(ARGV[2])
	local dedupe_ttl_seconds = tonumber(ARGV[3])
	local minimum_samples = tonumber(ARGV[4])
	local primary_window_ms = tonumber(ARGV[5])
	local fallback_window_ms = tonumber(ARGV[6])
	local fast_threshold_ms = tonumber(ARGV[7])
	local slow_threshold_ms = tonumber(ARGV[8])
	local confirmations = tonumber(ARGV[9])
	local request_id = ARGV[10]
	local now = redis.call('TIME')
	local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

	if redis.call('SET', dedupe_key, '1', 'NX', 'EX', dedupe_ttl_seconds) == false then
		return 0
	end

	redis.call('ZREMRANGEBYSCORE', samples_key, '-inf', now_ms - fallback_window_ms)
	local member = tostring(now_ms) .. ':' .. request_id .. ':' .. tostring(duration_ms)
	redis.call('ZADD', samples_key, now_ms, member)
	redis.call('EXPIRE', samples_key, stats_ttl_seconds)

	local function decode(raw_members)
		local values = {}
		for _, raw in ipairs(raw_members) do
			local value = tonumber(string.match(raw, ':(%d+)$'))
			if value then table.insert(values, value) end
		end
		return values
	end

	local primary = decode(redis.call('ZRANGEBYSCORE', samples_key, now_ms - primary_window_ms, '+inf'))
	local samples = primary
	local window_hours = 6
	if #samples < minimum_samples then
		samples = decode(redis.call('ZRANGEBYSCORE', samples_key, now_ms - fallback_window_ms, '+inf'))
		window_hours = 24
	end

	local old_updated = tonumber(redis.call('HGET', stats_key, 'updated_at_ms'))
	local is_fast = tonumber(redis.call('HGET', stats_key, 'is_fast')) or 0
	local enter_fast_streak = tonumber(redis.call('HGET', stats_key, 'enter_fast_streak')) or 0
	local exit_slow_streak = tonumber(redis.call('HGET', stats_key, 'exit_slow_streak')) or 0
	if not old_updated or now_ms - old_updated > primary_window_ms then
		is_fast = 0
		enter_fast_streak = 0
		exit_slow_streak = 0
	end

	if #samples < minimum_samples then
		redis.call('HDEL', stats_key, 'normal_total_ms', 'p50_ms', 'p90_ms')
		redis.call('HSET', stats_key,
			'sample_count', tostring(#samples),
			'window_hours', tostring(window_hours),
			'is_fast', '0',
			'enter_fast_streak', '0',
			'exit_slow_streak', '0',
			'updated_at_ms', tostring(now_ms),
			'score_version', '3')
	else
		table.sort(samples)
		local trim = math.floor(#samples * 0.10)
		local sum = 0
		for index = trim + 1, #samples - trim do sum = sum + samples[index] end
		local normal_total = sum / (#samples - 2 * trim)

		local function percentile(p)
			local position = (#samples - 1) * p + 1
			local lower = math.floor(position)
			local upper = math.ceil(position)
			if lower == upper then return samples[lower] end
			return samples[lower] + (position - lower) * (samples[upper] - samples[lower])
		end
		local p50 = percentile(0.50)
		local p90 = percentile(0.90)

		if normal_total <= fast_threshold_ms then
			exit_slow_streak = 0
			if is_fast == 0 then
				enter_fast_streak = enter_fast_streak + 1
				if enter_fast_streak >= confirmations then
					is_fast = 1
					enter_fast_streak = 0
				end
			else
				enter_fast_streak = 0
			end
		elseif normal_total >= slow_threshold_ms then
			enter_fast_streak = 0
			exit_slow_streak = exit_slow_streak + 1
			if is_fast == 1 and exit_slow_streak >= confirmations then
				is_fast = 0
			end
		else
			enter_fast_streak = 0
			exit_slow_streak = 0
		end

		redis.call('HSET', stats_key,
			'normal_total_ms', tostring(normal_total),
			'p50_ms', tostring(p50),
			'p90_ms', tostring(p90),
			'sample_count', tostring(#samples),
			'window_hours', tostring(window_hours),
			'is_fast', tostring(is_fast),
			'enter_fast_streak', tostring(enter_fast_streak),
			'exit_slow_streak', tostring(exit_slow_streak),
			'updated_at_ms', tostring(now_ms),
			'score_version', '3')
	end

	redis.call('EXPIRE', stats_key, stats_ttl_seconds)
	redis.call('DEL', probe_key)
	redis.call('DEL', manual_probe_key)
	return 1
`)

var totalLatencyManualProbeClaimScript = redis.NewScript(`
	local candidate_count = tonumber(ARGV[1])
	local lease_seconds = tonumber(ARGV[2])
	for index = 1, candidate_count do
		if redis.call('GET', KEYS[index]) then
			redis.call('DEL', KEYS[index])
			redis.call('SET', KEYS[candidate_count + index], '1', 'EX', lease_seconds)
			return index
		end
	end
	return 0
`)

type firstTokenLatencyStatsCache struct {
	rdb *redis.Client
}

// NewFirstTokenLatencyStatsCache retains the existing provider name for wire
// compatibility. Its data and behavior are total-duration based.
func NewFirstTokenLatencyStatsCache(rdb *redis.Client) service.FirstTokenLatencyStatsCache {
	return &firstTokenLatencyStatsCache{rdb: rdb}
}

func (c *firstTokenLatencyStatsCache) RecordSample(ctx context.Context, accountID int64, requestID string, durationMs int) error {
	requestID = strings.TrimSpace(requestID)
	if accountID <= 0 || requestID == "" || durationMs <= 0 {
		return nil
	}
	const statsTTL = 26 * time.Hour
	const dedupeTTL = 26 * time.Hour
	statsKey := fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID)
	samplesKey := fmt.Sprintf("%s%d", totalLatencySamplesPrefix, accountID)
	dedupeKey := fmt.Sprintf("scheduler:total_duration:event:%d:%s", accountID, requestID)
	probeKey := fmt.Sprintf("%s%d", totalLatencyProbePrefix, accountID)
	manualProbeKey := fmt.Sprintf("%s%d", totalLatencyManualProbePrefix, accountID)
	if _, err := totalLatencyStatsRecordScript.Run(
		ctx,
		c.rdb,
		[]string{statsKey, samplesKey, dedupeKey, probeKey, manualProbeKey},
		durationMs,
		int(statsTTL.Seconds()),
		int(dedupeTTL.Seconds()),
		20,
		int64((6*time.Hour)/time.Millisecond),
		int64((24*time.Hour)/time.Millisecond),
		12_000,
		16_000,
		3,
		requestID,
	).Result(); err != nil {
		return fmt.Errorf("record total-duration stats: %w", err)
	}
	return nil
}

func (c *firstTokenLatencyStatsCache) RequestManualProbe(ctx context.Context, accountID int64, ttl time.Duration) error {
	if accountID <= 0 || ttl <= 0 {
		return nil
	}
	if err := c.rdb.Set(ctx, fmt.Sprintf("%s%d", totalLatencyManualProbePrefix, accountID), "1", ttl).Err(); err != nil {
		return fmt.Errorf("queue total-duration manual probe: %w", err)
	}
	return nil
}

func (c *firstTokenLatencyStatsCache) TryClaimManualProbe(ctx context.Context, accountIDs []int64, lease time.Duration) (int64, bool, error) {
	if len(accountIDs) == 0 || lease <= 0 {
		return 0, false, nil
	}
	keys := make([]string, 0, len(accountIDs)*2)
	validAccountIDs := make([]int64, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		validAccountIDs = append(validAccountIDs, accountID)
		keys = append(keys, fmt.Sprintf("%s%d", totalLatencyManualProbePrefix, accountID))
	}
	if len(validAccountIDs) == 0 {
		return 0, false, nil
	}
	for _, accountID := range validAccountIDs {
		keys = append(keys, fmt.Sprintf("%s%d", totalLatencyProbePrefix, accountID))
	}
	claimedIndex, err := totalLatencyManualProbeClaimScript.Run(ctx, c.rdb, keys, len(validAccountIDs), int(lease.Seconds())).Int()
	if err != nil {
		return 0, false, fmt.Errorf("claim total-duration manual probe: %w", err)
	}
	if claimedIndex <= 0 || claimedIndex > len(validAccountIDs) {
		return 0, false, nil
	}
	return validAccountIDs[claimedIndex-1], true, nil
}

func (c *firstTokenLatencyStatsCache) TryClaimProbe(ctx context.Context, accountID int64, lease time.Duration) (bool, error) {
	if accountID <= 0 || lease <= 0 {
		return false, nil
	}
	claimed, err := c.rdb.SetNX(ctx, fmt.Sprintf("%s%d", totalLatencyProbePrefix, accountID), "1", lease).Result()
	if err != nil {
		return false, fmt.Errorf("claim total-duration probe: %w", err)
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
		commands[accountID] = pipe.HMGet(ctx, fmt.Sprintf("%s%d", totalLatencyStatsPrefix, accountID),
			"normal_total_ms", "p50_ms", "p90_ms", "sample_count", "window_hours",
			"updated_at_ms", "exit_slow_streak", "is_fast", "enter_fast_streak", "score_version")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get total-duration stats: %w", err)
	}
	for accountID, cmd := range commands {
		values, err := cmd.Result()
		if err != nil || len(values) != 10 || values[3] == nil || values[5] == nil {
			continue
		}
		count, countErr := strconv.ParseInt(fmt.Sprint(values[3]), 10, 64)
		updatedAtMS, updatedErr := strconv.ParseInt(fmt.Sprint(values[5]), 10, 64)
		if countErr != nil || updatedErr != nil || updatedAtMS <= 0 {
			continue
		}
		stat := service.FirstTokenLatencyStats{
			SampleCount:             count,
			UpdatedAt:               time.UnixMilli(updatedAtMS),
			ReliableFast:            values[7] != nil && fmt.Sprint(values[7]) == "1",
			FastConfirmationTracked: true,
		}
		if values[0] != nil && strings.TrimSpace(fmt.Sprint(values[0])) != "" {
			stat.PredictedMS, _ = strconv.ParseFloat(fmt.Sprint(values[0]), 64)
		}
		if values[1] != nil {
			stat.P50MS, _ = strconv.ParseFloat(fmt.Sprint(values[1]), 64)
		}
		if values[2] != nil {
			stat.P90MS, _ = strconv.ParseFloat(fmt.Sprint(values[2]), 64)
		}
		if values[4] != nil {
			stat.WindowHours, _ = strconv.Atoi(fmt.Sprint(values[4]))
		}
		if values[6] != nil {
			stat.SlowStreak, _ = strconv.Atoi(fmt.Sprint(values[6]))
		}
		if values[8] != nil {
			stat.RecoveryFastStreak, _ = strconv.Atoi(fmt.Sprint(values[8]))
		}
		result[accountID] = stat
	}
	return result, nil
}
