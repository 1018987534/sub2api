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

const (
	totalLatencyStatsPrefix       = "scheduler:total_duration:account:"
	totalLatencySamplesPrefix     = "scheduler:total_duration:samples:"
	totalLatencySlowSamplesPrefix = "scheduler:total_duration:slow_samples:"
	totalLatencyFastSamplesPrefix = "scheduler:total_duration:fast_samples:"
	totalLatencyProbePrefix       = "scheduler:total_duration:probe:"
	totalLatencyManualProbePrefix = "scheduler:total_duration:manual_probe:"

	totalLatencyTransitionWindow         = 3 * time.Minute
	totalLatencyExitMinimumSpan          = 2 * time.Minute
	totalLatencyExitRatioNumerator       = 7
	totalLatencyExitRatioDenominator     = 10
	totalLatencyExitRelativeNumerator    = 3
	totalLatencyExitRelativeDenominator  = 2
	totalLatencyExitMinimumDelta         = 5 * time.Second
	totalLatencyRecoveryMinimumSamples   = 4
	totalLatencyRecoveryMinimumSpan      = time.Minute
	totalLatencyRecoveryRatioNumerator   = 3
	totalLatencyRecoveryRatioDenominator = 4
)

// Each completed, billable stream updates one timestamped 24-hour window and
// atomically derives a stable bounded rolling average plus a bounded three-minute
// transition window. Fast-pool entry still requires confirmation; exit needs
// broad, abrupt degradation (without a fixed transition sample-count gate), while
// one fast slow-pool sample opens a short recovery-probe state without immediately
// promoting the account. A single completed request over the circuit threshold
// immediately removes an already-fast account from the fast pool.
var totalLatencyStatsRecordScript = redis.NewScript(`
	local stats_key = KEYS[1]
	local samples_key = KEYS[2]
	local slow_samples_key = KEYS[3]
	local fast_samples_key = KEYS[4]
	local dedupe_key = KEYS[5]
	local probe_key = KEYS[6]
	local manual_probe_key = KEYS[7]
	local duration_ms = tonumber(ARGV[1])
	local stats_ttl_seconds = tonumber(ARGV[2])
	local dedupe_ttl_seconds = tonumber(ARGV[3])
	local sample_limit = tonumber(ARGV[4])
	local minimum_samples = tonumber(ARGV[5])
	local primary_window_ms = tonumber(ARGV[6])
	local fallback_window_ms = tonumber(ARGV[7])
	local fast_threshold_ms = tonumber(ARGV[8])
	local slow_threshold_ms = tonumber(ARGV[9])
	local transition_window_ms = tonumber(ARGV[10])
	local exit_minimum_span_ms = tonumber(ARGV[11])
	local exit_ratio_numerator = tonumber(ARGV[12])
	local exit_ratio_denominator = tonumber(ARGV[13])
	local exit_relative_numerator = tonumber(ARGV[14])
	local exit_relative_denominator = tonumber(ARGV[15])
	local exit_minimum_delta_ms = tonumber(ARGV[16])
	local recovery_minimum_samples = tonumber(ARGV[17])
	local recovery_minimum_span_ms = tonumber(ARGV[18])
	local recovery_ratio_numerator = tonumber(ARGV[19])
	local recovery_ratio_denominator = tonumber(ARGV[20])
	local single_sample_circuit_ms = tonumber(ARGV[21])
	local request_id = ARGV[22]
	local now = redis.call('TIME')
	local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

	if redis.call('SET', dedupe_key, '1', 'NX', 'EX', dedupe_ttl_seconds) == false then
		return 0
	end

	redis.call('ZREMRANGEBYSCORE', samples_key, '-inf', now_ms - fallback_window_ms)
	redis.call('ZREMRANGEBYSCORE', slow_samples_key, '-inf', now_ms - fallback_window_ms)
	redis.call('ZREMRANGEBYSCORE', fast_samples_key, '-inf', now_ms - fallback_window_ms)
	local member = tostring(now_ms) .. ':' .. request_id .. ':' .. tostring(duration_ms)
	redis.call('ZADD', samples_key, now_ms, member)
	redis.call('EXPIRE', samples_key, stats_ttl_seconds)
	if duration_ms >= slow_threshold_ms then
		redis.call('ZADD', slow_samples_key, now_ms, member)
	end
	if duration_ms <= fast_threshold_ms then
		redis.call('ZADD', fast_samples_key, now_ms, member)
	end
	redis.call('EXPIRE', slow_samples_key, stats_ttl_seconds)
	redis.call('EXPIRE', fast_samples_key, stats_ttl_seconds)

	local transition_window_start_ms = now_ms - transition_window_ms
	local transition_sample_count = tonumber(redis.call('ZCOUNT', samples_key, transition_window_start_ms, '+inf')) or 0
	local transition_slow_count = tonumber(redis.call('ZCOUNT', slow_samples_key, transition_window_start_ms, '+inf')) or 0
	local transition_fast_count = tonumber(redis.call('ZCOUNT', fast_samples_key, transition_window_start_ms, '+inf')) or 0
	local transition_span_ms = 0
	local oldest_recent = redis.call('ZRANGEBYSCORE', samples_key, transition_window_start_ms, '+inf', 'WITHSCORES', 'LIMIT', 0, 1)
	if #oldest_recent >= 2 then
		transition_span_ms = now_ms - tonumber(oldest_recent[2])
	end

	local function decode(raw_members)
		local values = {}
		for _, raw in ipairs(raw_members) do
			local value = tonumber(string.match(raw, ':(%d+)$'))
			if value then table.insert(values, value) end
		end
		return values
	end
	local function median(values)
		if #values == 0 then return 0 end
		table.sort(values)
		local middle = math.floor(#values / 2)
		if #values % 2 == 0 then
			return (values[middle] + values[middle + 1]) / 2
		end
		return values[middle + 1]
	end
	local function average(values)
		if #values == 0 then return 0 end
		local total = 0
		for _, value in ipairs(values) do total = total + value end
		return total / #values
	end
	local function percentile(sorted_values, p)
		if #sorted_values == 0 then return 0 end
		local position = (#sorted_values - 1) * p + 1
		local lower = math.floor(position)
		local upper = math.ceil(position)
		if lower == upper then return sorted_values[lower] end
		return sorted_values[lower] + (position - lower) * (sorted_values[upper] - sorted_values[lower])
	end

	-- Prefer the configured primary window once it has enough observations. The
	-- 24-hour retained set is the stable fallback while the primary window warms up.
	local primary_count = tonumber(redis.call('ZCOUNT', samples_key, now_ms - primary_window_ms, '+inf')) or 0
	local use_primary = primary_count >= minimum_samples
	local window_hours = 24
	if use_primary then
		window_hours = math.floor(primary_window_ms / (60 * 60 * 1000))
	end
	local samples = decode(redis.call('ZREVRANGEBYSCORE', samples_key, '+inf', now_ms - (use_primary and primary_window_ms or fallback_window_ms), 'LIMIT', 0, sample_limit))
	local recent_samples = decode(redis.call('ZREVRANGEBYSCORE', samples_key, '+inf', transition_window_start_ms, 'LIMIT', 0, 99))
	local baseline_samples = decode(redis.call('ZREVRANGEBYSCORE', samples_key, transition_window_start_ms - 1, '-inf', 'LIMIT', 0, sample_limit))

	local old_normal_total = tonumber(redis.call('HGET', stats_key, 'normal_total_ms')) or 0
	local old_updated = tonumber(redis.call('HGET', stats_key, 'updated_at_ms'))
	local is_fast = tonumber(redis.call('HGET', stats_key, 'is_fast')) or 0
	local circuit_broken = tonumber(redis.call('HGET', stats_key, 'circuit_broken')) or 0
	local enter_fast_streak = tonumber(redis.call('HGET', stats_key, 'enter_fast_streak')) or 0
	local recovery_fast_streak = tonumber(redis.call('HGET', stats_key, 'recovery_fast_streak')) or 0
	local exit_slow_streak = tonumber(redis.call('HGET', stats_key, 'exit_slow_streak')) or 0
	if not old_updated or now_ms - old_updated > primary_window_ms then
		is_fast = 0
		circuit_broken = 0
		enter_fast_streak = 0
		recovery_fast_streak = 0
		exit_slow_streak = 0
	end

	local recent_total = median(recent_samples)
	local baseline_total = median(baseline_samples)
	if baseline_total <= 0 then baseline_total = old_normal_total end
	local exit_window_degraded = 0
	local abrupt_difference = baseline_total <= 0 or
		(recent_total * exit_relative_denominator >= baseline_total * exit_relative_numerator and
		recent_total >= baseline_total + exit_minimum_delta_ms)
	if transition_span_ms >= exit_minimum_span_ms and
		transition_slow_count * exit_ratio_denominator >= transition_sample_count * exit_ratio_numerator and
		recent_total >= slow_threshold_ms and abrupt_difference then
		exit_window_degraded = 1
	end
	local recovery_candidate = 0
	if duration_ms <= fast_threshold_ms or
		(transition_sample_count > 0 and transition_fast_count > 0 and
		transition_fast_count * 2 >= transition_sample_count) then
		recovery_candidate = 1
	end
	local recovery_window_confirmed = 0
	if transition_sample_count >= recovery_minimum_samples and
		transition_span_ms >= recovery_minimum_span_ms and
		transition_fast_count * recovery_ratio_denominator >= transition_sample_count * recovery_ratio_numerator and
		recent_total > 0 and recent_total <= fast_threshold_ms then
		recovery_window_confirmed = 1
	end

	if #samples == 0 then
		redis.call('HDEL', stats_key, 'normal_total_ms', 'p50_ms', 'p90_ms')
		redis.call('HSET', stats_key,
			'sample_count', tostring(#samples),
			'window_hours', tostring(window_hours),
			'is_fast', '0',
			'enter_fast_streak', '0',
			'recovery_fast_streak', '0',
			'exit_slow_streak', '0',
			'circuit_broken', tostring(circuit_broken),
			'updated_at_ms', tostring(now_ms),
			'baseline_total_ms', tostring(baseline_total),
			'recent_total_ms', tostring(recent_total),
			'exit_window_sample_count', tostring(transition_sample_count),
			'exit_window_slow_count', tostring(transition_slow_count),
			'exit_window_fast_count', tostring(transition_fast_count),
			'exit_window_span_ms', tostring(transition_span_ms),
			'exit_window_degraded', tostring(exit_window_degraded),
			'recovery_candidate', tostring(recovery_candidate),
			'recovery_window_confirmed', tostring(recovery_window_confirmed),
			'score_version', '8')
	else
		table.sort(samples)
		local normal_total = average(samples)
		local p50 = percentile(samples, 0.50)
		local p90 = percentile(samples, 0.90)
		local single_sample_circuit = is_fast == 1 and duration_ms > single_sample_circuit_ms
		if single_sample_circuit then
			is_fast = 0
			circuit_broken = 1
			enter_fast_streak = 0
			recovery_fast_streak = 0
			exit_slow_streak = 0
			recovery_candidate = 0
			recovery_window_confirmed = 0
		end

		if #samples < minimum_samples then
			is_fast = 0
			if recovery_candidate == 1 then
				recovery_fast_streak = transition_fast_count
			else
				recovery_fast_streak = 0
			end
			exit_slow_streak = 0
		elseif is_fast == 1 and exit_window_degraded == 1 then
			is_fast = 0
			enter_fast_streak = 0
			recovery_fast_streak = 0
			exit_slow_streak = transition_slow_count
			normal_total = recent_total
		elseif is_fast == 1 then
			enter_fast_streak = 0
			recovery_fast_streak = 0
			exit_slow_streak = 0
			if recovery_window_confirmed == 1 then
				normal_total = recent_total
			end
		else
			if recovery_candidate == 1 then
				recovery_fast_streak = transition_fast_count
			else
				recovery_fast_streak = 0
			end
			if recovery_window_confirmed == 1 then
				is_fast = 1
				circuit_broken = 0
				enter_fast_streak = 0
				recovery_fast_streak = 0
				exit_slow_streak = 0
				normal_total = recent_total
			else
				enter_fast_streak = 0
			end
			if is_fast == 0 and normal_total >= slow_threshold_ms then
				exit_slow_streak = exit_slow_streak + 1
			elseif is_fast == 0 then
				exit_slow_streak = 0
			end
		end

		redis.call('HSET', stats_key,
			'normal_total_ms', tostring(normal_total),
			'p50_ms', tostring(p50),
			'p90_ms', tostring(p90),
			'sample_count', tostring(#samples),
			'window_hours', tostring(window_hours),
			'is_fast', tostring(is_fast),
			'enter_fast_streak', tostring(enter_fast_streak),
			'recovery_fast_streak', tostring(recovery_fast_streak),
			'exit_slow_streak', tostring(exit_slow_streak),
			'circuit_broken', tostring(circuit_broken),
			'updated_at_ms', tostring(now_ms),
			'baseline_total_ms', tostring(baseline_total),
			'recent_total_ms', tostring(recent_total),
			'exit_window_sample_count', tostring(transition_sample_count),
			'exit_window_slow_count', tostring(transition_slow_count),
			'exit_window_fast_count', tostring(transition_fast_count),
			'exit_window_span_ms', tostring(transition_span_ms),
			'exit_window_degraded', tostring(exit_window_degraded),
			'recovery_candidate', tostring(recovery_candidate),
			'recovery_window_confirmed', tostring(recovery_window_confirmed),
			'score_version', '8')
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
	slowSamplesKey := fmt.Sprintf("%s%d", totalLatencySlowSamplesPrefix, accountID)
	fastSamplesKey := fmt.Sprintf("%s%d", totalLatencyFastSamplesPrefix, accountID)
	dedupeKey := fmt.Sprintf("scheduler:total_duration:event:%d:%s", accountID, requestID)
	probeKey := fmt.Sprintf("%s%d", totalLatencyProbePrefix, accountID)
	manualProbeKey := fmt.Sprintf("%s%d", totalLatencyManualProbePrefix, accountID)
	settings := service.CurrentTotalDurationSettings()
	if _, err := totalLatencyStatsRecordScript.Run(
		ctx,
		c.rdb,
		[]string{statsKey, samplesKey, slowSamplesKey, fastSamplesKey, dedupeKey, probeKey, manualProbeKey},
		durationMs,
		int(statsTTL.Seconds()),
		int(dedupeTTL.Seconds()),
		settings.SampleLimit,
		settings.MinimumSamples,
		int64((time.Duration(settings.PrimaryWindowHours)*time.Hour)/time.Millisecond),
		int64((24*time.Hour)/time.Millisecond),
		settings.FastThresholdSeconds*1000,
		settings.SlowThresholdSeconds*1000,
		int64(totalLatencyTransitionWindow/time.Millisecond),
		int64(totalLatencyExitMinimumSpan/time.Millisecond),
		totalLatencyExitRatioNumerator,
		totalLatencyExitRatioDenominator,
		totalLatencyExitRelativeNumerator,
		totalLatencyExitRelativeDenominator,
		int64(totalLatencyExitMinimumDelta/time.Millisecond),
		totalLatencyRecoveryMinimumSamples,
		int64(totalLatencyRecoveryMinimumSpan/time.Millisecond),
		totalLatencyRecoveryRatioNumerator,
		totalLatencyRecoveryRatioDenominator,
		settings.SingleSampleCircuitSeconds*1000,
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
			"updated_at_ms", "exit_slow_streak", "is_fast", "enter_fast_streak", "recovery_fast_streak", "score_version", "circuit_broken")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get total-duration stats: %w", err)
	}
	for accountID, cmd := range commands {
		values, err := cmd.Result()
		if err != nil || len(values) != 12 || values[3] == nil || values[5] == nil {
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
		recoveryValue := values[9]
		if recoveryValue == nil {
			recoveryValue = values[8]
		}
		if recoveryValue != nil {
			stat.RecoveryFastStreak, _ = strconv.Atoi(fmt.Sprint(recoveryValue))
		}
		if values[11] != nil {
			stat.CircuitBroken = fmt.Sprint(values[11]) == "1"
		}
		result[accountID] = stat
	}
	return result, nil
}
