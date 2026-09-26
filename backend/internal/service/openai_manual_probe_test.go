package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type manualProbeQueueTestCache struct {
	staticFirstTokenLatencyStatsCache
	pendingErr  error
	claimErr    error
	loseClaim   bool
	beforeClaim func()
}

func (c *manualProbeQueueTestCache) PendingManualProbeAccountIDs(context.Context) ([]int64, error) {
	if c.pendingErr != nil {
		return nil, c.pendingErr
	}
	if c.manualProbeID == 0 {
		return nil, nil
	}
	return []int64{c.manualProbeID}, nil
}

func (c *manualProbeQueueTestCache) TryClaimManualProbe(ctx context.Context, ids []int64, lease time.Duration) (int64, bool, error) {
	if c.beforeClaim != nil {
		c.beforeClaim()
	}
	if c.claimErr != nil || c.loseClaim {
		return 0, false, c.claimErr
	}
	return c.staticFirstTokenLatencyStatsCache.TryClaimManualProbe(ctx, ids, lease)
}

func TestOpenAIManualProbePreemptsStickyTransaction(t *testing.T) {
	now := time.Now()
	sticky := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.01, now, time.Hour)
	probe := upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 1, now, time.Hour)
	for _, account := range []*Account{sticky, probe} {
		account.Status = StatusActive
		account.Schedulable = true
		account.Concurrency = 1
	}
	stats := &manualProbeQueueTestCache{staticFirstTokenLatencyStatsCache: staticFirstTokenLatencyStatsCache{
		manualProbeID: probe.ID,
		stats: map[int64]FirstTokenLatencyStats{
			sticky.ID: {PredictedMS: 5000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
			probe.ID:  {PredictedMS: 60000, SampleCount: 20, UpdatedAt: now},
		},
	}}
	bindings := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:conversation": sticky.ID}}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{*sticky, *probe}},
		cache:              bindings,
		rateLimitService:   &RateLimitService{firstTokenLatencyStatsCache: stats},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	scheduler := newDefaultOpenAIAccountScheduler(svc, nil)
	selection, decision, err := scheduler.Select(context.Background(), OpenAIAccountScheduleRequest{
		Platform: PlatformOpenAI, SessionHash: "conversation", StickyAccountID: sticky.ID,
		StickyWeighted: true, FirstTokenPriority: true, FirstTokenProbeEligible: true,
	})
	require.NoError(t, err)
	require.NotNil(t, selection)
	defer selection.ReleaseFunc()
	t.Logf("selected=%d layer=%s queued=%d binding=%d", selection.Account.ID, decision.Layer, stats.manualProbeID, bindings.sessionBindings["openai:conversation"])
	require.Equal(t, probe.ID, selection.Account.ID)
	require.Zero(t, stats.manualProbeID)
	require.Equal(t, probe.ID, bindings.sessionBindings["openai:conversation"])
}

type manualProbeFixture struct {
	service  *OpenAIGatewayService
	queue    *manualProbeQueueTestCache
	bindings *schedulerTestGatewayCache
	accounts []Account
	slots    map[int64]bool
	acquired []int64
	released []int64
	req      OpenAIAccountScheduleRequest
}

func newManualProbeFixture(t *testing.T) *manualProbeFixture {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	now := time.Now()
	f := &manualProbeFixture{
		accounts: []Account{
			{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1},
			{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1},
		},
		queue: &manualProbeQueueTestCache{staticFirstTokenLatencyStatsCache: staticFirstTokenLatencyStatsCache{
			manualProbeID: 2,
			stats: map[int64]FirstTokenLatencyStats{
				1: {PredictedMS: 5000, SampleCount: 20, UpdatedAt: now, ReliableFast: true, FastConfirmationTracked: true},
				2: {PredictedMS: 60000, SampleCount: 20, UpdatedAt: now},
			},
		}},
		bindings: &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:conversation": 1}},
		slots:    map[int64]bool{1: true, 2: true},
		req: OpenAIAccountScheduleRequest{
			Platform: PlatformOpenAI, SessionHash: "conversation", StickyAccountID: 1,
			StickyWeighted: true, FirstTokenPriority: true, FirstTokenProbeEligible: true,
		},
	}
	f.service = &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: f.accounts},
		cache:       f.bindings,
		cfg:         &config.Config{},
		rateLimitService: &RateLimitService{
			firstTokenLatencyStatsCache: f.queue,
			settingService: NewSettingService(&openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
				SettingKeyFirstTokenPriorityEnabled: "true",
			}}, &config.Config{}),
		},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
			acquireResults: f.slots, acquiredIDs: &f.acquired, releasedIDs: &f.released,
		}),
	}
	return f
}

func TestOpenAIManualProbeScheduling(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*manualProbeFixture)
		want  int64
	}{
		{"hard session affinity", func(f *manualProbeFixture) { f.req.StickyWeighted = false }, 2},
		{"guardian affinity", func(f *manualProbeFixture) { f.req.GuardianParentAccountID = 1 }, 2},
		{"movable previous response", func(f *manualProbeFixture) {
			f.req.PreviousResponseID = "resp_movable"
			f.req.PreviousResponseCanMove = true
			f.req.StickyPreviousAccountID = 1
		}, 2},
		{"nonmovable previous response", func(f *manualProbeFixture) {
			f.req.PreviousResponseID = "resp_locked"
			f.req.PreviousResponseCanMove = false
			f.accounts[0].Extra = map[string]any{"openai_apikey_responses_websockets_v2_enabled": true}
			f.service.cfg = newSchedulerTestOpenAIWSV2Config()
			require.NoError(t, f.service.getOpenAIWSStateStore().BindResponseAccount(context.Background(), 0, "resp_locked", 1, time.Hour))
		}, 1},
		{"fresh scheduling", func(f *manualProbeFixture) { f.req.StickyAccountID = 0; f.req.SessionHash = "" }, 2},
		{"nonstreaming", func(f *manualProbeFixture) { f.req.FirstTokenProbeEligible = false }, 1},
		{"priority disabled", func(f *manualProbeFixture) { f.req.FirstTokenPriority = false; f.req.StickyWeighted = false }, 1},
		{"wrong group", func(f *manualProbeFixture) { f.accounts[1].GroupIDs = []int64{99} }, 1},
		{"wrong model", func(f *manualProbeFixture) {
			f.req.RequestedModel = "model-a"
			f.accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"model-b": "model-b"}}
		}, 1},
		{"excluded account", func(f *manualProbeFixture) { f.req.ExcludedIDs = map[int64]struct{}{2: {}} }, 1},
		{"disabled account", func(f *manualProbeFixture) { f.accounts[1].Schedulable = false }, 1},
		{"rate limited", func(f *manualProbeFixture) {
			reset := time.Now().Add(time.Hour)
			f.accounts[1].RateLimitResetAt = &reset
		}, 1},
		{"busy", func(f *manualProbeFixture) { f.slots[2] = false }, 1},
		{"queue unavailable", func(f *manualProbeFixture) { f.queue.pendingErr = errors.New("redis unavailable") }, 1},
		{"claim unavailable", func(f *manualProbeFixture) { f.queue.claimErr = errors.New("redis unavailable") }, 1},
		{"another gateway claimed", func(f *manualProbeFixture) { f.queue.loseClaim = true }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManualProbeFixture(t)
			tc.setup(f)
			selection, decision, err := newDefaultOpenAIAccountScheduler(f.service, nil).Select(context.Background(), f.req)
			require.NoError(t, err)
			require.NotNil(t, selection)
			defer selection.ReleaseFunc()
			require.Equal(t, tc.want, selection.Account.ID)
			if tc.want == 2 {
				require.Equal(t, "manual_probe", decision.Layer)
				require.Zero(t, f.queue.manualProbeID)
			} else {
				require.Equal(t, int64(2), f.queue.manualProbeID)
				require.Equal(t, int64(1), f.bindings.sessionBindings["openai:conversation"])
			}
			if f.queue.claimErr != nil || f.queue.loseClaim {
				require.Equal(t, []int64{2}, f.released, "failed claim must release its slot without rebinding")
			}
		})
	}
}

func TestOpenAIManualProbeWaitsForSlotThenClaimsOnce(t *testing.T) {
	f := newManualProbeFixture(t)
	scheduler := newDefaultOpenAIAccountScheduler(f.service, nil)
	f.slots[2] = false
	selection, _, err := scheduler.Select(context.Background(), f.req)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	selection.ReleaseFunc()
	require.Equal(t, int64(2), f.queue.manualProbeID)
	f.slots[2] = true
	f.acquired = nil
	f.queue.beforeClaim = func() {
		require.Equal(t, []int64{2}, f.acquired, "acquire must precede queue consumption")
		require.Equal(t, int64(1), f.bindings.sessionBindings["openai:conversation"], "claim must precede rebinding")
	}
	selection, decision, err := scheduler.Select(context.Background(), f.req)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, "manual_probe", decision.Layer)
	selection.ReleaseFunc()
	require.Zero(t, f.queue.manualProbeID)
	require.Equal(t, int64(2), f.bindings.sessionBindings["openai:conversation"])
	selection, decision, err = scheduler.Select(context.Background(), f.req)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID, "the manual override must be one-shot")
	require.NotEqual(t, "manual_probe", decision.Layer)
	selection.ReleaseFunc()
}

func TestOpenAIManualProbePublicSchedulingEntry(t *testing.T) {
	f := newManualProbeFixture(t)
	ctx := WithFirstTokenProbeEligibility(context.Background(), true)
	selection, decision, err := f.service.SelectAccountWithScheduler(ctx, nil, "", "conversation", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, "manual_probe", decision.Layer)
	require.Equal(t, int64(2), f.bindings.sessionBindings["openai:conversation"])
	selection.ReleaseFunc()
}
