package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const totalLatencyStatsPrefix = "scheduler:total_duration:account:"
const totalLatencySamplesPrefix = "scheduler:total_duration:samples:"
const totalLatencyProbePrefix = "scheduler:total_duration:probe:"
const totalLatencyManualProbePrefix = "scheduler:total_duration:manual_probe:"
const totalLatencyDimensionStatsPrefix = "scheduler:total_duration:dimension:account:"
const totalLatencyDimensionSamplesPrefix = "scheduler:total_duration:dimension:samples:"
const totalLatencyDimensionProbePrefix = "scheduler:total_duration:dimension:probe:"
const totalLatencyDimensionManualProbePrefix = "scheduler:total_duration:dimension:manual_probe:"
const totalLatencyFastThresholdMS = 17_000
const totalLatencySlowThresholdMS = 21_000

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
	local recent_window_ms = tonumber(ARGV[10])
	local recent_slow_threshold_ms = tonumber(ARGV[11])
	local recent_slow_ratio = tonumber(ARGV[12])
	local circuit_break_threshold_ms = tonumber(ARGV[13])
	local request_id = ARGV[14]
	local now = redis.call('TIME')
	local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

	if redis.call('SET', dedupe_key, '1', 'NX', 'EX', dedupe_ttl_seconds) == false then
		return 0
	end

	-- A single very long completed request is an immediate circuit break. Keep
	-- an explicit pending hash so the scheduler can expose the reset state and
	-- sticky selection cannot reuse the old fast-pool decision.
	local function reset_pending(circuit_broken)
		redis.call('DEL', stats_key, samples_key, probe_key, manual_probe_key)
		redis.call('HSET', stats_key,
			'sample_count', '0',
			'window_hours', '0',
			'is_fast', '0',
			'enter_fast_streak', '0',
			'exit_slow_streak', '0',
			'circuit_broken', tostring(circuit_broken),
			'updated_at_ms', tostring(now_ms),
			'score_version', '4')
		redis.call('EXPIRE', stats_key, stats_ttl_seconds)
	end

	if circuit_break_threshold_ms > 0 and duration_ms > circuit_break_threshold_ms then
		reset_pending(1)
		return 3
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
	local recent = decode(redis.call('ZRANGEBYSCORE', samples_key, now_ms - recent_window_ms, '+inf'))
	local recent_slow = 0
	for _, value in ipairs(recent) do
		if value > recent_slow_threshold_ms then recent_slow = recent_slow + 1 end
	end
	local recent_overload = #recent > 0 and (recent_slow / #recent) > recent_slow_ratio
	if recent_overload and is_fast == 1 then
		-- A fast pool that suddenly produces too many minute-plus requests is
		-- considered untrusted. Drop the whole rolling state so it re-enters
		-- as "pending collection" instead of carrying stale fast samples.
		reset_pending(0)
		return 2
	end

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
			'circuit_broken', '0',
			'updated_at_ms', tostring(now_ms),
			'score_version', '4')
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
		elseif normal_total > slow_threshold_ms then
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
			'circuit_broken', '0',
			'updated_at_ms', tostring(now_ms),
			'score_version', '4')
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
	rdb      *redis.Client
	policyMu sync.RWMutex
	policy   service.TotalDurationLatencyPolicy
}

// NewFirstTokenLatencyStatsCache retains the existing provider name for wire
// compatibility. Its data and behavior are total-duration based.
func NewFirstTokenLatencyStatsCache(rdb *redis.Client) service.FirstTokenLatencyStatsCache {
	return &firstTokenLatencyStatsCache{rdb: rdb, policy: service.DefaultTotalDurationLatencyPolicy()}
}

func (c *firstTokenLatencyStatsCache) ConfigureTotalDurationLatencyPolicy(policy service.TotalDurationLatencyPolicy) {
	if c == nil {
		return
	}
	c.policyMu.Lock()
	c.policy = service.NormalizeTotalDurationLatencyPolicy(policy)
	c.policyMu.Unlock()
}

func (c *firstTokenLatencyStatsCache) totalDurationLatencyPolicy() service.TotalDurationLatencyPolicy {
	c.policyMu.RLock()
	policy := c.policy
	c.policyMu.RUnlock()
	return service.NormalizeTotalDurationLatencyPolicy(policy)
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
	policy := c.totalDurationLatencyPolicy()
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
		totalLatencyFastThresholdMS,
		totalLatencySlowThresholdMS,
		3,
		int64(policy.RecentWindow/time.Millisecond),
		int64(policy.RecentSlowThreshold/time.Millisecond),
		policy.RecentSlowRatio,
		int64(policy.CircuitBreakThreshold/time.Millisecond),
		requestID,
	).Result(); err != nil {
		return fmt.Errorf("record total-duration stats: %w", err)
	}
	return nil
}

func totalDurationDimensionKey(prefix string, dimension service.TotalDurationLatencyDimension) string {
	dimension = service.NormalizeTotalDurationLatencyDimension(dimension)
	return fmt.Sprintf("%s%d", prefix, dimension.AccountID)
}

func (c *firstTokenLatencyStatsCache) RecordSampleForDimension(ctx context.Context, dimension service.TotalDurationLatencyDimension, requestID string, durationMs int) error {
	dimension = service.NormalizeTotalDurationLatencyDimension(dimension)
	requestID = strings.TrimSpace(requestID)
	if dimension.AccountID <= 0 || requestID == "" || durationMs <= 0 {
		return nil
	}
	const statsTTL = 26 * time.Hour
	const dedupeTTL = 26 * time.Hour
	statsKey := totalDurationDimensionKey(totalLatencyDimensionStatsPrefix, dimension)
	samplesKey := totalDurationDimensionKey(totalLatencyDimensionSamplesPrefix, dimension)
	dedupeKey := fmt.Sprintf("scheduler:total_duration:dimension:event:%d:%s", dimension.AccountID, requestID)
	probeKey := totalDurationDimensionKey(totalLatencyDimensionProbePrefix, dimension)
	manualProbeKey := totalDurationDimensionKey(totalLatencyDimensionManualProbePrefix, dimension)
	policy := c.totalDurationLatencyPolicy()
	if _, err := totalLatencyStatsRecordScript.Run(ctx, c.rdb,
		[]string{statsKey, samplesKey, dedupeKey, probeKey, manualProbeKey},
		durationMs, int(statsTTL.Seconds()), int(dedupeTTL.Seconds()), 20,
		int64((6*time.Hour)/time.Millisecond), int64((24*time.Hour)/time.Millisecond),
		totalLatencyFastThresholdMS, totalLatencySlowThresholdMS, 3,
		int64(policy.RecentWindow/time.Millisecond), int64(policy.RecentSlowThreshold/time.Millisecond),
		policy.RecentSlowRatio, int64(policy.CircuitBreakThreshold/time.Millisecond), requestID).Result(); err != nil {
		return fmt.Errorf("record dimension total-duration stats: %w", err)
	}
	return c.rdb.HSet(ctx, statsKey, "account_id", dimension.AccountID).Err()
}

func (c *firstTokenLatencyStatsCache) TryClaimProbeForDimension(ctx context.Context, dimension service.TotalDurationLatencyDimension, lease time.Duration) (bool, error) {
	dimension = service.NormalizeTotalDurationLatencyDimension(dimension)
	if dimension.AccountID <= 0 || lease <= 0 {
		return false, nil
	}
	claimed, err := c.rdb.SetNX(ctx, totalDurationDimensionKey(totalLatencyDimensionProbePrefix, dimension), "1", lease).Result()
	if err != nil {
		return false, fmt.Errorf("claim dimension total-duration probe: %w", err)
	}
	return claimed, nil
}

func (c *firstTokenLatencyStatsCache) RequestManualProbeForDimension(ctx context.Context, dimension service.TotalDurationLatencyDimension, ttl time.Duration) error {
	dimension = service.NormalizeTotalDurationLatencyDimension(dimension)
	if dimension.AccountID <= 0 || ttl <= 0 {
		return nil
	}
	return c.rdb.Set(ctx, totalDurationDimensionKey(totalLatencyDimensionManualProbePrefix, dimension), "1", ttl).Err()
}

func (c *firstTokenLatencyStatsCache) TryClaimManualProbeForDimensions(ctx context.Context, dimensions []service.TotalDurationLatencyDimension, lease time.Duration) (service.TotalDurationLatencyDimension, bool, error) {
	if len(dimensions) == 0 || lease <= 0 {
		return service.TotalDurationLatencyDimension{}, false, nil
	}
	normalized := make([]service.TotalDurationLatencyDimension, 0, len(dimensions))
	queuedKeys := make([]string, 0, len(dimensions))
	for _, dimension := range dimensions {
		dimension = service.NormalizeTotalDurationLatencyDimension(dimension)
		if dimension.AccountID <= 0 {
			continue
		}
		normalized = append(normalized, dimension)
		queuedKeys = append(queuedKeys, totalDurationDimensionKey(totalLatencyDimensionManualProbePrefix, dimension))
	}
	if len(normalized) == 0 {
		return service.TotalDurationLatencyDimension{}, false, nil
	}
	keys := append([]string(nil), queuedKeys...)
	for _, dimension := range normalized {
		keys = append(keys, totalDurationDimensionKey(totalLatencyDimensionProbePrefix, dimension))
	}
	claimedIndex, err := totalLatencyManualProbeClaimScript.Run(ctx, c.rdb, keys, len(normalized), int(lease.Seconds())).Int()
	if err != nil {
		return service.TotalDurationLatencyDimension{}, false, fmt.Errorf("claim dimension total-duration manual probe: %w", err)
	}
	if claimedIndex <= 0 || claimedIndex > len(normalized) {
		return service.TotalDurationLatencyDimension{}, false, nil
	}
	return normalized[claimedIndex-1], true, nil
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
			"updated_at_ms", "exit_slow_streak", "is_fast", "enter_fast_streak", "score_version", "circuit_broken")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get total-duration stats: %w", err)
	}
	for accountID, cmd := range commands {
		values, err := cmd.Result()
		if err != nil {
			continue
		}
		if stat, ok := parseTotalDurationStats(values); ok {
			result[accountID] = stat
		}
	}
	return result, nil
}

func parseTotalDurationStats(values []interface{}) (service.FirstTokenLatencyStats, bool) {
	if (len(values) != 10 && len(values) != 11) || values[3] == nil || values[5] == nil {
		return service.FirstTokenLatencyStats{}, false
	}
	count, countErr := strconv.ParseInt(fmt.Sprint(values[3]), 10, 64)
	updatedAtMS, updatedErr := strconv.ParseInt(fmt.Sprint(values[5]), 10, 64)
	if countErr != nil || updatedErr != nil || updatedAtMS <= 0 {
		return service.FirstTokenLatencyStats{}, false
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
	if len(values) > 10 && values[10] != nil {
		stat.CircuitBroken = fmt.Sprint(values[10]) == "1"
	}
	return stat, true
}

func (c *firstTokenLatencyStatsCache) GetStatsBatchForDimensions(ctx context.Context, dimensions []service.TotalDurationLatencyDimension) (map[service.TotalDurationLatencyDimension]service.FirstTokenLatencyStats, error) {
	result := make(map[service.TotalDurationLatencyDimension]service.FirstTokenLatencyStats, len(dimensions))
	if len(dimensions) == 0 {
		return result, nil
	}
	pipe := c.rdb.Pipeline()
	commands := make(map[service.TotalDurationLatencyDimension]*redis.SliceCmd, len(dimensions))
	for _, dimension := range dimensions {
		dimension = service.NormalizeTotalDurationLatencyDimension(dimension)
		if dimension.AccountID <= 0 {
			continue
		}
		commands[dimension] = pipe.HMGet(ctx, totalDurationDimensionKey(totalLatencyDimensionStatsPrefix, dimension),
			"normal_total_ms", "p50_ms", "p90_ms", "sample_count", "window_hours",
			"updated_at_ms", "exit_slow_streak", "is_fast", "enter_fast_streak", "score_version", "circuit_broken")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get dimension total-duration stats: %w", err)
	}
	for dimension, command := range commands {
		values, err := command.Result()
		if err != nil {
			continue
		}
		if stat, ok := parseTotalDurationStats(values); ok {
			result[dimension] = stat
		}
	}
	return result, nil
}

func parseTotalDurationDimensionKey(key string) (service.TotalDurationLatencyDimension, bool) {
	value := strings.TrimPrefix(key, totalLatencyDimensionStatsPrefix)
	accountID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || accountID <= 0 {
		return service.TotalDurationLatencyDimension{}, false
	}
	return service.NormalizeTotalDurationLatencyDimension(service.TotalDurationLatencyDimension{AccountID: accountID}), true
}

func (c *firstTokenLatencyStatsCache) ListStatsByAccountIDs(ctx context.Context, accountIDs []int64) ([]service.TotalDurationLatencyMetric, error) {
	allowed := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID > 0 {
			allowed[accountID] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return []service.TotalDurationLatencyMetric{}, nil
	}
	// Dimension keys are retained for compatibility, but are now account-scoped.
	// Never use KEYS here because this endpoint is called from the admin
	// dashboard and must not block Redis while scanning a production database.
	var keys []string
	for cursor := uint64(0); ; {
		batch, nextCursor, err := c.rdb.Scan(ctx, cursor, totalLatencyDimensionStatsPrefix+"*", 200).Result()
		if err != nil {
			return nil, fmt.Errorf("scan dimension total-duration stats: %w", err)
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	metrics := make([]service.TotalDurationLatencyMetric, 0, len(keys))
	for _, key := range keys {
		dimension, ok := parseTotalDurationDimensionKey(key)
		if !ok {
			continue
		}
		if _, ok := allowed[dimension.AccountID]; !ok {
			continue
		}
		values, err := c.rdb.HMGet(ctx, key,
			"normal_total_ms", "p50_ms", "p90_ms", "sample_count", "window_hours",
			"updated_at_ms", "exit_slow_streak", "is_fast", "enter_fast_streak", "score_version", "circuit_broken").Result()
		if err != nil {
			continue
		}
		if stat, ok := parseTotalDurationStats(values); ok {
			metrics = append(metrics, service.TotalDurationLatencyMetric{Dimension: dimension, Stats: stat})
		}
	}
	return metrics, nil
}
