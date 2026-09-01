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
const totalLatencyRecentSamplesPrefix = "scheduler:total_duration:recent_samples:"
const totalLatencyProbePrefix = "scheduler:total_duration:probe:"
const totalLatencyManualProbePrefix = "scheduler:total_duration:manual_probe:"
const totalLatencyMaxSamples = 10
const totalLatencyMinimumSamples = 10

// Each completed, billable stream updates a bounded latest-request sample set
// and atomically derives the scheduling score. The score is the direct median
// of the latest ten requests; no wall-clock window or trimming is involved.
// Fast-pool exit reacts to a short burst of slow requests in the latest
// five-minute slice, or to three consecutive slow median updates. Recovery
// still requires three consecutive fast medians.
var totalLatencyStatsRecordScript = redis.NewScript(`
	local stats_key = KEYS[1]
	local samples_key = KEYS[2]
	local recent_samples_key = KEYS[3]
	local dedupe_key = KEYS[4]
	local probe_key = KEYS[5]
	local manual_probe_key = KEYS[6]
	local duration_ms = tonumber(ARGV[1])
	local stats_ttl_seconds = tonumber(ARGV[2])
	local dedupe_ttl_seconds = tonumber(ARGV[3])
	local max_samples = tonumber(ARGV[4])
	local minimum_samples = tonumber(ARGV[5])
	local fast_threshold_ms = tonumber(ARGV[6])
	local slow_threshold_ms = tonumber(ARGV[7])
	local confirmations = tonumber(ARGV[8])
	local request_id = ARGV[9]
	local now = redis.call('TIME')
	local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

	if redis.call('SET', dedupe_key, '1', 'NX', 'EX', dedupe_ttl_seconds) == false then
		return 0
	end

	local latest = redis.call('ZREVRANGE', samples_key, 0, 0, 'WITHSCORES')
	local event_score = 1
	if #latest >= 2 then event_score = tonumber(latest[2]) + 1 end
	-- Keep the completion timestamp in the member so the bounded latest-request
	-- set can also answer the short five-minute anomaly question.
	local member = tostring(event_score) .. ':' .. tostring(now_ms) .. ':' .. request_id .. ':' .. tostring(duration_ms)
	redis.call('ZADD', samples_key, event_score, member)
	local retained_count = redis.call('ZCARD', samples_key)
	if retained_count > max_samples then
		redis.call('ZREMRANGEBYRANK', samples_key, 0, retained_count - max_samples - 1)
	end
	redis.call('EXPIRE', samples_key, stats_ttl_seconds)
	-- Keep an independent wall-clock slice for burst detection. It is not
	-- bounded to ten entries because busy accounts can exceed ten requests.
	local recent_member = tostring(now_ms) .. ':' .. tostring(event_score) .. ':' .. request_id .. ':' .. tostring(duration_ms)
	redis.call('ZADD', recent_samples_key, now_ms, recent_member)
	redis.call('ZREMRANGEBYSCORE', recent_samples_key, '-inf', now_ms - 5 * 60 * 1000)
	redis.call('EXPIRE', recent_samples_key, 10 * 60)

	local function decode(raw_members, recent_format)
		local values = {}
		for _, raw in ipairs(raw_members) do
			local timestamp, sequence, value
			if recent_format then
				timestamp, sequence, value = string.match(raw, '^(%d+):(%d+):.*:(%d+)$')
			else
				timestamp, value = string.match(raw, '^%d+:(%d+):.*:(%d+)$')
			end
			if value then
				table.insert(values, {duration_ms = tonumber(value), timestamp_ms = tonumber(timestamp) or 0, sequence = tonumber(sequence) or 0})
			else
				-- Read members written by the previous latest-ten draft. They still
				-- contribute to the median, but cannot be classified as recent.
				value = string.match(raw, ':(%d+)$')
				if value then table.insert(values, {duration_ms = tonumber(value), timestamp_ms = 0}) end
			end
		end
		return values
	end

	local samples = decode(redis.call('ZRANGE', samples_key, 0, -1), false)
	local recent_samples = decode(redis.call('ZRANGE', recent_samples_key, 0, -1), true)
	local durations = {}
	local recent_count = #recent_samples
	local recent_slow_count = 0
	local consecutive_slow = 0
	local max_consecutive_slow = 0
	table.sort(recent_samples, function(left, right)
		if left.sequence == right.sequence then return left.timestamp_ms < right.timestamp_ms end
		return left.sequence < right.sequence
	end)
	for _, sample in ipairs(recent_samples) do
		if sample.duration_ms >= slow_threshold_ms then
			recent_slow_count = recent_slow_count + 1
			consecutive_slow = consecutive_slow + 1
			if consecutive_slow > max_consecutive_slow then max_consecutive_slow = consecutive_slow end
		else
			consecutive_slow = 0
		end
	end
	for _, sample in ipairs(samples) do table.insert(durations, sample.duration_ms) end

	local old_updated = tonumber(redis.call('HGET', stats_key, 'updated_at_ms'))
	local is_fast = tonumber(redis.call('HGET', stats_key, 'is_fast')) or 0
	local enter_fast_streak = tonumber(redis.call('HGET', stats_key, 'enter_fast_streak')) or 0
	local exit_slow_streak = tonumber(redis.call('HGET', stats_key, 'exit_slow_streak')) or 0
	if not old_updated or now_ms - old_updated > 6 * 60 * 60 * 1000 then
		is_fast = 0
		enter_fast_streak = 0
		exit_slow_streak = 0
	end
	local short_slowdown = is_fast == 1 and recent_count >= 3 and
		(recent_slow_count * 100 >= recent_count * 60 or max_consecutive_slow >= 3)

	if #samples < minimum_samples then
		redis.call('HDEL', stats_key, 'normal_total_ms', 'p50_ms', 'p90_ms')
		redis.call('HSET', stats_key,
			'sample_count', tostring(#samples),
			'sample_window_size', tostring(max_samples),
			'is_fast', '0',
			'enter_fast_streak', '0',
			'exit_slow_streak', '0',
			'updated_at_ms', tostring(now_ms),
			'score_version', '4')
	else
		table.sort(durations)
		local function percentile(p)
			local position = (#durations - 1) * p + 1
			local lower = math.floor(position)
			local upper = math.ceil(position)
			if lower == upper then return durations[lower] end
			return durations[lower] + (position - lower) * (durations[upper] - durations[lower])
		end
		local normal_total = percentile(0.50)
		local p50 = percentile(0.50)
		local p90 = percentile(0.90)

		if short_slowdown then
			-- The short window already supplies the three-request confirmation;
			-- do not wait for three more aggregate updates.
			is_fast = 0
			enter_fast_streak = 0
			exit_slow_streak = confirmations
		else
			if normal_total <= fast_threshold_ms then
				-- Preserve the short-window eviction marker while the triggering
				-- slow samples are still inside the five-minute slice. Clear it
				-- only after that slice has no slow observations left.
				if recent_slow_count == 0 or exit_slow_streak < confirmations then
					exit_slow_streak = 0
				end
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
		end

		redis.call('HSET', stats_key,
			'normal_total_ms', tostring(normal_total),
			'p50_ms', tostring(p50),
			'p90_ms', tostring(p90),
			'sample_count', tostring(#samples),
			'sample_window_size', tostring(max_samples),
			'is_fast', tostring(is_fast),
			'enter_fast_streak', tostring(enter_fast_streak),
			'exit_slow_streak', tostring(exit_slow_streak),
			'updated_at_ms', tostring(now_ms),
			'score_version', '4')
	end

	redis.call('EXPIRE', stats_key, stats_ttl_seconds)
	redis.call('EXPIRE', recent_samples_key, 10 * 60)
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
	recentSamplesKey := fmt.Sprintf("%s%d", totalLatencyRecentSamplesPrefix, accountID)
	dedupeKey := fmt.Sprintf("scheduler:total_duration:event:%d:%s", accountID, requestID)
	probeKey := fmt.Sprintf("%s%d", totalLatencyProbePrefix, accountID)
	manualProbeKey := fmt.Sprintf("%s%d", totalLatencyManualProbePrefix, accountID)
	if _, err := totalLatencyStatsRecordScript.Run(
		ctx,
		c.rdb,
		[]string{statsKey, samplesKey, recentSamplesKey, dedupeKey, probeKey, manualProbeKey},
		durationMs,
		int(statsTTL.Seconds()),
		int(dedupeTTL.Seconds()),
		totalLatencyMaxSamples,
		totalLatencyMinimumSamples,
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
			"normal_total_ms", "p50_ms", "p90_ms", "sample_count", "sample_window_size",
			"updated_at_ms", "exit_slow_streak", "is_fast", "enter_fast_streak", "score_version")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get total-duration stats: %w", err)
	}
	for accountID, cmd := range commands {
		values, err := cmd.Result()
		if err != nil || len(values) != 10 || values[3] == nil || values[5] == nil || values[9] == nil || fmt.Sprint(values[9]) != "4" {
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
			stat.SampleWindowSize, _ = strconv.Atoi(fmt.Sprint(values[4]))
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
