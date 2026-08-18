package repository

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const firstTokenLatencyStatsPrefix = "scheduler:first_token:account:"
const firstTokenLatencyProbePrefix = "scheduler:first_token:probe:"
const firstTokenLatencyManualProbePrefix = "scheduler:first_token:manual_probe:"

// Scheduling uses a time-smoothed robust score. The recent median absorbs
// isolated samples, while explicit confirmation lets recovered accounts return
// to the fast pool without inheriting stale slow history.
var firstTokenLatencyStatsRecordScript = redis.NewScript(`
	local stats_key = KEYS[1]
	local dedupe_key = KEYS[2]
	local probe_key = KEYS[3]
	local manual_probe_key = KEYS[4]
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
	local score_version = tonumber(redis.call('HGET', stats_key, 'score_version')) or 0
	local old_confirmation_tracked = tonumber(redis.call('HGET', stats_key, 'fast_confirmation_tracked')) or 0
	local recovery_fast_streak = tonumber(redis.call('HGET', stats_key, 'recovery_fast_streak')) or 0
	local recovery_fast_samples = redis.call('HGET', stats_key, 'recovery_fast_samples')
	local old_updated = tonumber(redis.call('HGET', stats_key, 'updated_at_ms'))
	local encoded = redis.call('HGET', stats_key, 'recent_samples')
	local sample_count = tonumber(redis.call('HGET', stats_key, 'sample_count')) or 0
	local retained_sample_count = 0
	local elapsed_seconds = 0
	if old_updated and now_ms > old_updated then
		elapsed_seconds = (now_ms - old_updated) / 1000
	end

	-- Average-based versions may leave a stale median_ms behind. Rebuild it from
	-- the retained sample window whenever the window exists.
	if encoded then
		local previous = {}
		for value in string.gmatch(encoded, '[^,]+') do
			local parsed = tonumber(value)
			if parsed then table.insert(previous, parsed) end
		end
		table.sort(previous)
		retained_sample_count = #previous
		if #previous > 0 then
			if #previous % 2 == 0 then
				old_median = (previous[#previous / 2] + previous[#previous / 2 + 1]) / 2
			else
				old_median = previous[math.floor((#previous + 1) / 2)]
			end
		end
	end
	if score_version < 2 and old_median then
		-- Migrate the old per-sample EWMA without inheriting its volatile score.
		old_ewma = old_median
	end
	local was_confirmed_fast = old_confirmation_tracked == 1 and old_reliable_fast == 1
	-- Existing production hashes predate confirmation tracking. Preserve fast
	-- state only when at least three retained observations support the median.
	if old_confirmation_tracked == 0 and old_reliable_fast == 1 and old_median and old_median <= 10000 and retained_sample_count >= 3 then
		was_confirmed_fast = true
	end
	if elapsed_seconds > 1200 then
		was_confirmed_fast = false
		recovery_fast_streak = 0
		recovery_fast_samples = nil
		sample_count = 0
		old_ewma = nil
	end
	local samples = {}
	if encoded and elapsed_seconds <= 1200 then
		for value in string.gmatch(encoded, '[^,]+') do
			table.insert(samples, tonumber(value))
		end
	end
	table.insert(samples, latency_ms)
	while #samples > 9 do
		table.remove(samples, 1)
	end

	sample_count = sample_count + 1

	local recovery_samples = {}
	if recovery_fast_samples then
		for value in string.gmatch(recovery_fast_samples, '[^,]+') do
			table.insert(recovery_samples, tonumber(value))
		end
	end
	if not was_confirmed_fast and latency_ms <= 10000 then
		recovery_fast_streak = recovery_fast_streak + 1
		table.insert(recovery_samples, latency_ms)
		while #recovery_samples > 3 do table.remove(recovery_samples, 1) end
	else
		recovery_fast_streak = 0
		recovery_samples = {}
	end
	local recovery_confirmed = not was_confirmed_fast and recovery_fast_streak >= 3
	if recovery_confirmed then
		samples = {}
		for _, value in ipairs(recovery_samples) do table.insert(samples, value) end
	end

	local sorted = {}
	for i, value in ipairs(samples) do sorted[i] = value end
	table.sort(sorted)
	local median = 0
	if #sorted > 0 then
		if #sorted % 2 == 0 then
			median = (sorted[#sorted / 2] + sorted[#sorted / 2 + 1]) / 2
		else
			median = sorted[math.floor((#sorted + 1) / 2)]
		end
	end
	local ewma = median
	if old_ewma and not recovery_confirmed then
		-- Time-based EWMA avoids traffic volume changing the smoothing strength.
		-- Busy and idle accounts converge on the same wall-clock horizon.
		local alpha = 1 - math.exp(-elapsed_seconds / 900)
		if alpha < 0 then alpha = 0 end
		ewma = alpha * median + (1 - alpha) * old_ewma
	end
	local reliable_fast = 0
	if (was_confirmed_fast or recovery_confirmed) and ewma > 0 and ewma <= 10000 then reliable_fast = 1 end
	if recovery_confirmed then
		recovery_fast_streak = 0
		recovery_samples = {}
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
	local recovery_parts = {}
	for i, value in ipairs(recovery_samples) do recovery_parts[i] = tostring(value) end
	redis.call('HSET', stats_key,
		'ewma_ms', tostring(ewma),
		'median_ms', tostring(median),
		'sample_count', tostring(sample_count),
		'reliable_fast', tostring(reliable_fast),
		'fast_confirmation_tracked', '1',
		'recovery_fast_streak', tostring(recovery_fast_streak),
		'recovery_fast_samples', table.concat(recovery_parts, ','),
		'score_version', '2',
		'slow_streak', tostring(slow_streak),
		'updated_at_ms', tostring(now_ms),
		'recent_samples', table.concat(parts, ','))
	redis.call('EXPIRE', stats_key, stats_ttl_seconds)
	redis.call('DEL', probe_key)
	redis.call('DEL', manual_probe_key)
	return 1
`)

var firstTokenManualProbeClaimScript = redis.NewScript(`
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
	manualProbeKey := fmt.Sprintf("%s%d", firstTokenLatencyManualProbePrefix, accountID)
	if _, err := firstTokenLatencyStatsRecordScript.Run(ctx, c.rdb, []string{statsKey, dedupeKey, probeKey, manualProbeKey}, firstTokenMs, int(statsTTL.Seconds()), int(dedupeTTL.Seconds())).Result(); err != nil {
		return fmt.Errorf("record first-token latency stats: %w", err)
	}
	return nil
}

func (c *firstTokenLatencyStatsCache) RequestManualProbe(ctx context.Context, accountID int64, ttl time.Duration) error {
	if accountID <= 0 || ttl <= 0 {
		return nil
	}
	if err := c.rdb.Set(ctx, fmt.Sprintf("%s%d", firstTokenLatencyManualProbePrefix, accountID), "1", ttl).Err(); err != nil {
		return fmt.Errorf("queue first-token manual probe: %w", err)
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
		keys = append(keys, fmt.Sprintf("%s%d", firstTokenLatencyManualProbePrefix, accountID))
	}
	if len(validAccountIDs) == 0 {
		return 0, false, nil
	}
	for _, accountID := range validAccountIDs {
		keys = append(keys, fmt.Sprintf("%s%d", firstTokenLatencyProbePrefix, accountID))
	}
	claimedIndex, err := firstTokenManualProbeClaimScript.Run(ctx, c.rdb, keys, len(validAccountIDs), int(lease.Seconds())).Int()
	if err != nil {
		return 0, false, fmt.Errorf("claim first-token manual probe: %w", err)
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
		commands[accountID] = pipe.HMGet(ctx, fmt.Sprintf("%s%d", firstTokenLatencyStatsPrefix, accountID), "ewma_ms", "median_ms", "sample_count", "updated_at_ms", "slow_streak", "reliable_fast", "recent_samples", "fast_confirmation_tracked", "recovery_fast_streak", "score_version")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get first-token latency stats: %w", err)
	}
	for accountID, cmd := range commands {
		values, err := cmd.Result()
		if err != nil || len(values) != 10 || values[0] == nil {
			continue
		}
		stablePrediction, ewmaErr := strconv.ParseFloat(fmt.Sprint(values[0]), 64)
		var median float64
		var medianErr error
		recentSampleCount := 0
		if values[6] != nil && strings.TrimSpace(fmt.Sprint(values[6])) != "" {
			samples := make([]float64, 0, 9)
			for _, raw := range strings.Split(fmt.Sprint(values[6]), ",") {
				value, parseErr := strconv.ParseFloat(strings.TrimSpace(raw), 64)
				if parseErr == nil {
					samples = append(samples, value)
				}
			}
			if len(samples) > 0 {
				recentSampleCount = len(samples)
				sort.Float64s(samples)
				middle := len(samples) / 2
				if len(samples)%2 == 0 {
					median = (samples[middle-1] + samples[middle]) / 2
				} else {
					median = samples[middle]
				}
			} else {
				medianErr = fmt.Errorf("recent samples are empty")
			}
		} else if values[1] != nil {
			median, medianErr = strconv.ParseFloat(fmt.Sprint(values[1]), 64)
		} else {
			medianErr = fmt.Errorf("median samples are missing")
		}
		count, countErr := strconv.ParseInt(fmt.Sprint(values[2]), 10, 64)
		updatedAtMS, updatedErr := strconv.ParseInt(fmt.Sprint(values[3]), 10, 64)
		slowStreak, streakErr := strconv.Atoi(fmt.Sprint(values[4]))
		reliableFast := values[5] != nil && fmt.Sprint(values[5]) == "1"
		confirmationTracked := values[7] != nil && fmt.Sprint(values[7]) == "1"
		if !confirmationTracked {
			// Older average-based versions could reset recent_samples to one good
			// probe while retaining a large cumulative sample_count. Treat that
			// hash as migrated in memory, but do not inherit fast status unless the
			// retained window itself contains enough supporting observations.
			confirmationTracked = true
			reliableFast = reliableFast && recentSampleCount >= 3 && median <= 10_000
		}
		recoveryFastStreak := 0
		if values[8] != nil {
			recoveryFastStreak, _ = strconv.Atoi(fmt.Sprint(values[8]))
		}
		scoreVersion := 0
		if values[9] != nil {
			scoreVersion, _ = strconv.Atoi(fmt.Sprint(values[9]))
		}
		if scoreVersion < 2 {
			stablePrediction = median
		}
		if ewmaErr != nil || medianErr != nil || countErr != nil || updatedErr != nil || streakErr != nil || count <= 0 || updatedAtMS <= 0 {
			continue
		}
		result[accountID] = service.FirstTokenLatencyStats{
			PredictedMS:             stablePrediction,
			SampleCount:             count,
			UpdatedAt:               time.UnixMilli(updatedAtMS),
			SlowStreak:              slowStreak,
			ReliableFast:            reliableFast,
			FastConfirmationTracked: confirmationTracked,
			RecoveryFastStreak:      recoveryFastStreak,
		}
	}
	return result, nil
}
