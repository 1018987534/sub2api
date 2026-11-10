package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type usageRepoStub struct {
	UsageLogRepository
	stats      *usagestats.DashboardStats
	rangeStats *usagestats.DashboardStats
	err        error
	rangeErr   error
	calls      int32
	rangeCalls int32
	rangeStart time.Time
	rangeEnd   time.Time
	onCall     chan struct{}
}

func (s *usageRepoStub) GetDashboardStats(ctx context.Context) (*usagestats.DashboardStats, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.onCall != nil {
		select {
		case s.onCall <- struct{}{}:
		default:
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.stats, nil
}

func (s *usageRepoStub) GetDashboardStatsWithRange(ctx context.Context, start, end time.Time) (*usagestats.DashboardStats, error) {
	atomic.AddInt32(&s.rangeCalls, 1)
	s.rangeStart = start
	s.rangeEnd = end
	if s.rangeErr != nil {
		return nil, s.rangeErr
	}
	if s.rangeStats != nil {
		return s.rangeStats, nil
	}
	return s.stats, nil
}

type dashboardCacheStub struct {
	get       func(ctx context.Context) (string, error)
	set       func(ctx context.Context, data string, ttl time.Duration) error
	del       func(ctx context.Context) error
	getCalls  int32
	setCalls  int32
	delCalls  int32
	lastSetMu sync.Mutex
	lastSet   string
}

func (c *dashboardCacheStub) GetDashboardStats(ctx context.Context) (string, error) {
	atomic.AddInt32(&c.getCalls, 1)
	if c.get != nil {
		return c.get(ctx)
	}
	return "", ErrDashboardStatsCacheMiss
}

func (c *dashboardCacheStub) SetDashboardStats(ctx context.Context, data string, ttl time.Duration) error {
	atomic.AddInt32(&c.setCalls, 1)
	c.lastSetMu.Lock()
	c.lastSet = data
	c.lastSetMu.Unlock()
	if c.set != nil {
		return c.set(ctx, data, ttl)
	}
	return nil
}

func (c *dashboardCacheStub) DeleteDashboardStats(ctx context.Context) error {
	atomic.AddInt32(&c.delCalls, 1)
	if c.del != nil {
		return c.del(ctx)
	}
	return nil
}

type dashboardAggregationRepoStub struct {
	watermark time.Time
	err       error
}

func (s *dashboardAggregationRepoStub) AggregateRange(ctx context.Context, start, end time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoStub) RecomputeRange(ctx context.Context, start, end time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoStub) GetAggregationWatermark(ctx context.Context) (time.Time, error) {
	if s.err != nil {
		return time.Time{}, s.err
	}
	return s.watermark, nil
}

func (s *dashboardAggregationRepoStub) UpdateAggregationWatermark(ctx context.Context, aggregatedAt time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoStub) CleanupAggregates(ctx context.Context, hourlyCutoff, dailyCutoff time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoStub) CleanupUsageLogs(ctx context.Context, cutoff time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoStub) CleanupUsageBillingDedup(ctx context.Context, cutoff time.Time) error {
	return nil
}

func (s *dashboardAggregationRepoStub) EnsureUsageLogsPartitions(ctx context.Context, now time.Time) error {
	return nil
}

func (c *dashboardCacheStub) readLastEntry(t *testing.T) dashboardStatsCacheEntry {
	t.Helper()
	c.lastSetMu.Lock()
	data := c.lastSet
	c.lastSetMu.Unlock()

	var entry dashboardStatsCacheEntry
	err := json.Unmarshal([]byte(data), &entry)
	require.NoError(t, err)
	return entry
}

func TestDashboardService_CacheHitFresh(t *testing.T) {
	stats := &usagestats.DashboardStats{
		TotalUsers:     10,
		StatsUpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339),
		StatsStale:     true,
	}
	entry := dashboardStatsCacheEntry{
		Stats:     stats,
		UpdatedAt: time.Now().Unix(),
	}
	payload, err := json.Marshal(entry)
	require.NoError(t, err)

	cache := &dashboardCacheStub{
		get: func(ctx context.Context) (string, error) {
			return string(payload), nil
		},
	}
	repo := &usageRepoStub{
		stats: &usagestats.DashboardStats{TotalUsers: 99},
	}
	aggRepo := &dashboardAggregationRepoStub{watermark: time.Unix(0, 0).UTC()}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: true},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: true,
		},
	}
	svc := NewDashboardService(repo, aggRepo, cache, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, stats, got)
	require.Equal(t, int32(0), atomic.LoadInt32(&repo.calls))
	require.Equal(t, int32(1), atomic.LoadInt32(&cache.getCalls))
	require.Equal(t, int32(0), atomic.LoadInt32(&cache.setCalls))
}

func TestDashboardService_CacheMiss_StoresCache(t *testing.T) {
	stats := &usagestats.DashboardStats{
		TotalUsers:     7,
		StatsUpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339),
		StatsStale:     true,
	}
	cache := &dashboardCacheStub{
		get: func(ctx context.Context) (string, error) {
			return "", ErrDashboardStatsCacheMiss
		},
	}
	repo := &usageRepoStub{stats: stats}
	aggRepo := &dashboardAggregationRepoStub{watermark: time.Unix(0, 0).UTC()}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: true},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: true,
		},
	}
	svc := NewDashboardService(repo, aggRepo, cache, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, stats, got)
	require.Equal(t, int32(1), atomic.LoadInt32(&repo.calls))
	require.Equal(t, int32(1), atomic.LoadInt32(&cache.getCalls))
	require.Equal(t, int32(1), atomic.LoadInt32(&cache.setCalls))
	entry := cache.readLastEntry(t)
	require.Equal(t, stats, entry.Stats)
	require.WithinDuration(t, time.Now(), time.Unix(entry.UpdatedAt, 0), time.Second)
}

func TestDashboardService_CacheDisabled_SkipsCache(t *testing.T) {
	stats := &usagestats.DashboardStats{
		TotalUsers:     3,
		StatsUpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339),
		StatsStale:     true,
	}
	cache := &dashboardCacheStub{
		get: func(ctx context.Context) (string, error) {
			return "", nil
		},
	}
	repo := &usageRepoStub{stats: stats}
	aggRepo := &dashboardAggregationRepoStub{watermark: time.Unix(0, 0).UTC()}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: false},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: true,
		},
	}
	svc := NewDashboardService(repo, aggRepo, cache, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, stats, got)
	require.Equal(t, int32(1), atomic.LoadInt32(&repo.calls))
	require.Equal(t, int32(0), atomic.LoadInt32(&cache.getCalls))
	require.Equal(t, int32(0), atomic.LoadInt32(&cache.setCalls))
}

func TestDashboardService_CacheHitStale_TriggersAsyncRefresh(t *testing.T) {
	staleStats := &usagestats.DashboardStats{
		TotalUsers:     11,
		StatsUpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339),
		StatsStale:     true,
	}
	entry := dashboardStatsCacheEntry{
		Stats:     staleStats,
		UpdatedAt: time.Now().Add(-defaultDashboardStatsFreshTTL * 2).Unix(),
	}
	payload, err := json.Marshal(entry)
	require.NoError(t, err)

	cache := &dashboardCacheStub{
		get: func(ctx context.Context) (string, error) {
			return string(payload), nil
		},
	}
	refreshCh := make(chan struct{}, 1)
	repo := &usageRepoStub{
		stats:  &usagestats.DashboardStats{TotalUsers: 22},
		onCall: refreshCh,
	}
	aggRepo := &dashboardAggregationRepoStub{watermark: time.Unix(0, 0).UTC()}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: true},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: true,
		},
	}
	svc := NewDashboardService(repo, aggRepo, cache, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, staleStats, got)

	select {
	case <-refreshCh:
	case <-time.After(1 * time.Second):
		t.Fatal("等待异步刷新超时")
	}
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.setCalls) >= 1
	}, 1*time.Second, 10*time.Millisecond)
}

func TestDashboardService_CacheParseError_EvictsAndRefetches(t *testing.T) {
	cache := &dashboardCacheStub{
		get: func(ctx context.Context) (string, error) {
			return "not-json", nil
		},
	}
	stats := &usagestats.DashboardStats{TotalUsers: 9}
	repo := &usageRepoStub{stats: stats}
	aggRepo := &dashboardAggregationRepoStub{watermark: time.Unix(0, 0).UTC()}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: true},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: true,
		},
	}
	svc := NewDashboardService(repo, aggRepo, cache, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, stats, got)
	require.Equal(t, int32(1), atomic.LoadInt32(&cache.delCalls))
	require.Equal(t, int32(1), atomic.LoadInt32(&repo.calls))
}

func TestDashboardService_CacheParseError_RepoFailure(t *testing.T) {
	cache := &dashboardCacheStub{
		get: func(ctx context.Context) (string, error) {
			return "not-json", nil
		},
	}
	repo := &usageRepoStub{err: errors.New("db down")}
	aggRepo := &dashboardAggregationRepoStub{watermark: time.Unix(0, 0).UTC()}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: true},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: true,
		},
	}
	svc := NewDashboardService(repo, aggRepo, cache, cfg, nil)

	_, err := svc.GetDashboardStats(context.Background())
	require.Error(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&cache.delCalls))
}

func TestDashboardService_StatsUpdatedAtEpochWhenMissing(t *testing.T) {
	stats := &usagestats.DashboardStats{}
	repo := &usageRepoStub{stats: stats}
	aggRepo := &dashboardAggregationRepoStub{watermark: time.Unix(0, 0).UTC()}
	cfg := &config.Config{Dashboard: config.DashboardCacheConfig{Enabled: false}}
	svc := NewDashboardService(repo, aggRepo, nil, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, "1970-01-01T00:00:00Z", got.StatsUpdatedAt)
	require.True(t, got.StatsStale)
}

func TestDashboardService_StatsStaleFalseWhenFresh(t *testing.T) {
	aggNow := time.Now().UTC().Truncate(time.Second)
	stats := &usagestats.DashboardStats{}
	repo := &usageRepoStub{stats: stats}
	aggRepo := &dashboardAggregationRepoStub{watermark: aggNow}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: false},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled:         true,
			IntervalSeconds: 60,
			LookbackSeconds: 120,
		},
	}
	svc := NewDashboardService(repo, aggRepo, nil, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, aggNow.Format(time.RFC3339), got.StatsUpdatedAt)
	require.False(t, got.StatsStale)
}

func TestDashboardService_AggDisabled_UsesUsageLogsFallback(t *testing.T) {
	expected := &usagestats.DashboardStats{TotalUsers: 42}
	repo := &usageRepoStub{
		rangeStats: expected,
		err:        errors.New("should not call aggregated stats"),
	}
	cfg := &config.Config{
		Dashboard: config.DashboardCacheConfig{Enabled: false},
		DashboardAgg: config.DashboardAggregationConfig{
			Enabled: false,
			Retention: config.DashboardAggregationRetentionConfig{
				UsageLogsDays: 7,
			},
		},
	}
	svc := NewDashboardService(repo, nil, nil, cfg, nil)

	got, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(42), got.TotalUsers)
	require.Equal(t, int32(0), atomic.LoadInt32(&repo.calls))
	require.Equal(t, int32(1), atomic.LoadInt32(&repo.rangeCalls))
	require.False(t, repo.rangeEnd.IsZero())
	require.Equal(t, truncateToDayUTC(repo.rangeEnd.AddDate(0, 0, -7)), repo.rangeStart)
}

type dashboardProfitRepoStub struct {
	UsageLogRepository
	usage map[int64]*usagestats.BatchUserUsageStats
	err   error
	ids   []int64
	start time.Time
	end   time.Time
}

func (r *dashboardProfitRepoStub) GetBatchUserUsageStats(_ context.Context, ids []int64, start, end time.Time) (map[int64]*usagestats.BatchUserUsageStats, error) {
	r.ids, r.start, r.end = ids, start, end
	return r.usage, r.err
}

func TestDashboardService_ProfitExcludesCurrentAdminActualCost(t *testing.T) {
	stats := &usagestats.DashboardStats{TodayActualCost: 315.88, TodayAccountCost: 202}
	repo := &dashboardProfitRepoStub{usage: map[int64]*usagestats.BatchUserUsageStats{
		7: {UserID: 7, TodayActualCost: 20, TotalActualCost: 9999},
		8: {UserID: 8, TodayActualCost: 100},
	}}
	svc := NewDashboardService(repo, nil, nil, nil, nil)
	before := time.Now().UTC()
	profit, err := svc.GetDashboardProfitStats(context.Background(), 7, stats)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, repo.ids)
	require.Equal(t, timezone.StartOfMonth(timezone.Now()), repo.start)
	require.WithinRange(t, repo.end, before, time.Now().UTC())
	require.Equal(t, 20.0, profit.TodayAdminActualCost)
	require.Equal(t, 9999.0, profit.MonthAdminActualCost)
	require.InDelta(t, 5.32584, profit.TodayWithdrawalFee, 1e-9)
	require.InDelta(t, 88.55416, profit.TodayProfit, 1e-9)
	require.Equal(t, 315.88, stats.TodayActualCost, "shared dashboard statistics must stay unchanged")
}

func TestDashboardService_ProfitAllowsLossAndNoSelfUsage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actual  float64
		cost    float64
		own     float64
		lottery float64
		qq      float64
		fee     float64
		want    float64
	}{
		{"loss", 10, 12, 3, 0, 0, 0.126, -5.126},
		{"no self usage", 315.88, 202, 0, 0, 0, 5.68584, 108.19416},
		{"empty day", 0, 0, 0, 0, 0, 0, 0},
		{"lottery payout", 315.88, 202, 20, 5, 0, 5.32584, 83.55416},
		{"lottery creates loss", 10, 5, 2, 10, 0, 0.144, -7.144},
		{"QQ newcomer and invitation payouts", 315.88, 202, 20, 5, 4, 5.32584, 79.55416},
		{"QQ creates loss", 10, 5, 2, 0, 4, 0.144, -1.144},
		{"QQ on empty revenue day", 0, 0, 0, 0, 4, 0, -4},
		{"administrator only", 20, 8, 20, 0, 0, 0, -8},
		{"administrator exceeds total", 10, 5, 12, 0, 0, 0, -7},
		{"screenshot", 251.93, 136.25, 3.61, 0, 0, 4.46976, 107.60024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &dashboardProfitRepoStub{usage: map[int64]*usagestats.BatchUserUsageStats{}}
			if tc.own != 0 {
				repo.usage[7] = &usagestats.BatchUserUsageStats{TodayActualCost: tc.own}
			}
			svc := NewDashboardService(repo, nil, nil, nil, nil)
			stats := &usagestats.DashboardStats{TodayActualCost: tc.actual, TodayAccountCost: tc.cost, TodayLotteryCost: tc.lottery, TodayQQRewardCost: tc.qq}
			profit, err := svc.GetDashboardProfitStats(context.Background(), 7, stats)
			require.NoError(t, err)
			require.InDelta(t, tc.fee, profit.TodayWithdrawalFee, 1e-9)
			require.InDelta(t, tc.want, profit.TodayProfit, 1e-9)
			require.Equal(t, tc.qq, stats.TodayQQRewardCost, "shared reward statistics must stay unchanged")
		})
	}
}

func TestDashboardService_ProfitFailsWhenSelfUsageUnavailable(t *testing.T) {
	repo := &dashboardProfitRepoStub{err: errors.New("usage database unavailable")}
	svc := NewDashboardService(repo, nil, nil, nil, nil)
	_, err := svc.GetDashboardProfitStats(context.Background(), 7, &usagestats.DashboardStats{})
	require.ErrorContains(t, err, "usage database unavailable")
	_, err = svc.GetDashboardProfitStats(context.Background(), 0, &usagestats.DashboardStats{})
	require.ErrorContains(t, err, "authenticated administrator")
	_, err = svc.GetDashboardProfitStats(context.Background(), 7, nil)
	require.ErrorContains(t, err, "statistics")
}

func TestDashboardService_ProfitDeductsMonthlyServerCost(t *testing.T) {
	ctx := context.Background()
	settingsRepo := &dashboardProfitSettingsRepo{}
	settingsService := NewSettingService(settingsRepo, nil)
	repo := &dashboardProfitRepoStub{usage: map[int64]*usagestats.BatchUserUsageStats{
		7: {UserID: 7, TodayActualCost: 3.61},
	}}
	svc := NewDashboardService(repo, nil, nil, nil, settingsService)
	stats := &usagestats.DashboardStats{TodayActualCost: 251.93, TodayAccountCost: 136.25, TodayLotteryCost: 5, TodayQQRewardCost: 4}
	for _, monthly := range []float64{600, 6000, 0} {
		saved, err := settingsService.UpdateDashboardProfitSettings(ctx, dashboardProfitMonthlyUpdate(monthly))
		require.NoError(t, err)
		profit, err := svc.GetDashboardProfitStats(ctx, 7, stats)
		require.NoError(t, err)
		require.InDelta(t, saved.DailyServerCostUSD, profit.TodayServerCostUSD, 1e-9)
		require.InDelta(t, 4.46976, profit.TodayWithdrawalFee, 1e-9, "server expense must not change the withdrawal fee base")
		require.InDelta(t, 98.60024-saved.DailyServerCostUSD, profit.TodayProfit, 1e-9)
		require.InDelta(t, saved.DailyServerCostUSD*float64(timezone.Now().Day()), profit.MonthServerCostUSD, 1e-9)
		require.Equal(t, 251.93, stats.TodayActualCost)
	}
	settingsRepo.getErr = errors.New("server expense unavailable")
	_, err := svc.GetDashboardProfitStats(ctx, 7, stats)
	require.ErrorContains(t, err, "server expense unavailable")
}

func TestDashboardService_MonthProfitUsesMonthTotals(t *testing.T) {
	repo := &dashboardProfitRepoStub{usage: map[int64]*usagestats.BatchUserUsageStats{
		7: {TodayActualCost: 20, TotalActualCost: 100},
	}}
	stats := &usagestats.DashboardStats{
		TodayActualCost: 315.88, TodayAccountCost: 202, TodayLotteryCost: 5, TodayQQRewardCost: 4,
		MonthActualCost: 1000, MonthAccountCost: 600, MonthLotteryCost: 10, MonthQQRewardCost: 20,
	}
	profit, err := NewDashboardService(repo, nil, nil, nil, nil).GetDashboardProfitStats(context.Background(), 7, stats)
	require.NoError(t, err)
	require.Equal(t, timezone.StartOfMonth(timezone.Now()), repo.start)
	require.Equal(t, 100.0, profit.MonthAdminActualCost)
	require.InDelta(t, 16.2, profit.MonthWithdrawalFee, 1e-9)
	require.InDelta(t, 253.8, profit.MonthProfit, 1e-9)
	require.InDelta(t, 79.55416, profit.TodayProfit, 1e-9)
	require.Equal(t, 1000.0, stats.MonthActualCost, "administrator profit must not mutate shared statistics")
}

func TestDashboardService_MonthServerCostUsesCalendarDaysThroughToday(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		at   time.Time
		want float64
	}{
		{"first day", time.Date(2026, 12, 1, 0, 0, 0, 0, loc), 600.0 / 31},
		{"eighth day", time.Date(2026, 11, 8, 0, 0, 0, 0, loc), 160},
		{"short month", time.Date(2026, 2, 7, 12, 0, 0, 0, loc), 150},
		{"leap month end", time.Date(2028, 2, 29, 23, 59, 0, 0, loc), 600},
		{"local month boundary", time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC).In(loc), 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := dashboardProfitSettingsForMonth(600, tc.at)
			profit := &usagestats.DashboardProfitStats{MonthAdminActualCost: 100}
			stats := &usagestats.DashboardStats{MonthActualCost: 1000, MonthAccountCost: 600, MonthLotteryCost: 10, MonthQQRewardCost: 20}
			fillDashboardMonthProfit(stats, profit, settings.DailyServerCostUSD, defaultDashboardWithdrawalFeeRatePercent, tc.at)
			require.InDelta(t, tc.want, profit.MonthServerCostUSD, 1e-9)
			require.InDelta(t, 253.8-tc.want, profit.MonthProfit, 1e-9)
			require.InDelta(t, 16.2, profit.MonthWithdrawalFee, 1e-9)
		})
	}
}

func TestDashboardService_MonthProfitAllowsLossAndClampsFee(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		actual, cost, own, qq, wantFee, wantProfit float64
	}{
		{"loss", 10, 12, 3, 4, 0.126, -9.126},
		{"empty month", 0, 0, 0, 0, 0, 0},
		{"administrator exceeds total", 10, 5, 12, 0, 0, -7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profit := &usagestats.DashboardProfitStats{MonthAdminActualCost: tc.own}
			fillDashboardMonthProfit(&usagestats.DashboardStats{MonthActualCost: tc.actual, MonthAccountCost: tc.cost, MonthQQRewardCost: tc.qq}, profit, 0, defaultDashboardWithdrawalFeeRatePercent, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
			require.InDelta(t, tc.wantFee, profit.MonthWithdrawalFee, 1e-9)
			require.InDelta(t, tc.wantProfit, profit.MonthProfit, 1e-9)
		})
	}
}

func TestDashboardService_ProfitUsesSavedWithdrawalFeeForTodayAndMonth(t *testing.T) {
	ctx := context.Background()
	settingsRepo := &dashboardProfitSettingsRepo{}
	settingsService := NewSettingService(settingsRepo, nil)
	repo := &dashboardProfitRepoStub{usage: map[int64]*usagestats.BatchUserUsageStats{7: {TodayActualCost: 20, TotalActualCost: 100}}}
	stats := &usagestats.DashboardStats{
		TodayActualCost: 315.88, TodayAccountCost: 202, TodayLotteryCost: 5, TodayQQRewardCost: 4,
		MonthActualCost: 1000, MonthAccountCost: 600, MonthLotteryCost: 10, MonthQQRewardCost: 20,
	}
	for _, tc := range []struct{ rate, todayFee, monthFee, todayProfit, monthProfit float64 }{
		{1.8, 5.32584, 16.2, 79.55416, 253.8},
		{3.2, 9.46816, 28.8, 75.41184, 241.2},
		{0, 0, 0, 84.88, 270},
		{100, 295.88, 900, -211, -630},
	} {
		_, err := settingsService.UpdateDashboardProfitSettings(ctx, DashboardProfitSettingsUpdate{WithdrawalFeeRatePercent: &tc.rate})
		require.NoError(t, err)
		// A fresh service must read the persisted setting without cached defaults.
		svc := NewDashboardService(repo, nil, nil, nil, NewSettingService(settingsRepo, nil))
		profit, err := svc.GetDashboardProfitStats(ctx, 7, stats)
		require.NoError(t, err)
		require.Equal(t, tc.rate, profit.WithdrawalFeeRatePercent)
		require.InDelta(t, tc.todayFee, profit.TodayWithdrawalFee, 1e-9)
		require.InDelta(t, tc.monthFee, profit.MonthWithdrawalFee, 1e-9)
		require.InDelta(t, tc.todayProfit, profit.TodayProfit, 1e-9)
		require.InDelta(t, tc.monthProfit, profit.MonthProfit, 1e-9)
	}
	stats.TodayActualCost, stats.MonthActualCost = 10, 50
	profit, err := NewDashboardService(repo, nil, nil, nil, settingsService).GetDashboardProfitStats(ctx, 7, stats)
	require.NoError(t, err)
	require.Zero(t, profit.TodayWithdrawalFee)
	require.Zero(t, profit.MonthWithdrawalFee)
}
