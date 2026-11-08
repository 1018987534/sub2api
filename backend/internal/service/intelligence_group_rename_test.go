package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceGroupRenameReconcilesIsolation(t *testing.T) {
	dsn := os.Getenv("INTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	for _, scenario := range []string{"alternative", "sole", "model_incompatible"} {
		t.Run(scenario, func(t *testing.T) {
			openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil))
			t.Cleanup(func() { openAIAdvancedSchedulerSettingCache.Store((*cachedOpenAIAdvancedSchedulerSetting)(nil)) })
			ctx := context.Background()
			oldGroup := Group{ID: 79, Name: "020不降智", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1}
			renamed := oldGroup
			renamed.ID = 107
			renamed.Name = "030企业满血"
			a, b := cacheRateTestAccount(1), cacheRateTestAccount(2)
			a.GroupIDs = []int64{79, 107}
			a.Groups = []*Group{&oldGroup, &renamed}
			generation := time.Now().Add(20 * time.Minute).Format(time.RFC3339Nano)
			a.Extra = map[string]any{intelligencePauseUntilKey: generation, intelligenceRecoveryRequiredKey: true, intelligenceProtectedGroupsKey: []int64{79}, intelligenceAllowedGroupsKey: []int64{}}
			repo := &sharedIntelligenceRepo{intelligenceProtectionRepo: intelligenceProtectionRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*a, *b}}, groups: []Group{oldGroup, renamed}}, byGroup: map[int64][]int64{79: {1, 2}, 107: {1, 2}}}
			if scenario == "sole" {
				repo.byGroup[107] = []int64{1}
			}
			if scenario == "model_incompatible" {
				repo.accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"different-model": "different-model"}}
			}
			cache := &intelligenceResetCache{staticFirstTokenLatencyStatsCache: staticFirstTokenLatencyStatsCache{stats: map[int64]FirstTokenLatencyStats{1: {PredictedMS: 8000, P50MS: 7500, P90MS: 10000, SampleCount: 24, WindowHours: 6, UpdatedAt: time.Now(), ReliableFast: true, FastConfirmationTracked: true}}}}
			settings := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{SettingKeyTotalDurationPriorityEnabled: "true"}}
			svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{firstTokenLatencyStatsCache: cache, usageRepo: cacheRateProviderStub{stats: map[int64]AccountCacheStats{}}, settingService: &SettingService{settingRepo: settings}}}
			account, err := repo.GetByID(ctx, 1)
			require.NoError(t, err)
			metric := func() AccountFirstTokenLatencyMetric {
				metrics, err := svc.rateLimitService.AccountFirstTokenLatencyMetrics(ctx, []Account{*account})
				require.NoError(t, err)
				require.Len(t, metrics, 1)
				return metrics[0]
			}
			groupMetric := func(m AccountFirstTokenLatencyMetric) AccountFirstTokenLatencyGroup {
				for _, g := range m.Groups {
					if g.GroupID == 107 {
						return g
					}
				}
				t.Fatal("missing group 107")
				return AccountFirstTokenLatencyGroup{}
			}
			before := metric()
			require.False(t, groupMetric(before).IntelligenceBlocked)
			require.False(t, groupMetric(before).IntelligenceExempt)
			renamed.Name = "030企业满血不降智"
			repo.groups[1] = renamed
			pending := groupMetric(metric())
			t.Logf("RENAMED pending blocked=%v exempt=%v protected=%v allowed=%v", pending.IntelligenceBlocked, pending.IntelligenceExempt, intelligenceGroupIDList(account.Extra[intelligenceProtectedGroupsKey]), intelligenceAllowedGroups(account))
			require.False(t, pending.IntelligenceExempt, "missing protection scope must not be labelled a sole-account exemption")
			db, err := sql.Open("postgres", dsn)
			require.NoError(t, err)
			defer db.Close()
			db.SetMaxOpenConns(1)
			_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts(id BIGINT,temp_unschedulable_until TIMESTAMPTZ,temp_unschedulable_reason TEXT,extra JSONB,deleted_at TIMESTAMPTZ);CREATE TEMP TABLE groups(id BIGINT,name TEXT,platform TEXT,status TEXT,deleted_at TIMESTAMPTZ);CREATE TEMP TABLE account_groups(account_id BIGINT,group_id BIGINT);CREATE TEMP TABLE intelligence_check_runs(id BIGSERIAL PRIMARY KEY,account_id BIGINT,status TEXT,config_snapshot JSONB,protection_eligible BOOLEAN);SET search_path=pg_temp;`)
			require.NoError(t, err)
			extra, err := json.Marshal(account.Extra)
			require.NoError(t, err)
			config := intelligence.DefaultConfig(79)
			config.Model = "gpt-6.1-sol"
			raw, err := json.Marshal(config)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO groups VALUES(79,'020不降智','openai','active',NULL),(107,'030企业满血不降智','openai','active',NULL);INSERT INTO account_groups VALUES(1,79),(1,107);`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO accounts(id,extra)VALUES(1,$1)`, string(extra))
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO intelligence_check_runs(account_id,status,config_snapshot,protection_eligible)VALUES(1,'degraded',$1,true)`, string(raw))
			require.NoError(t, err)
			store := &intelligence.SQLStore{DB: db, Protect: func(ctx context.Context, c intelligence.Config, id int64) (bool, error) {
				require.Equal(t, config.Model, c.Model)
				return svc.ProtectDegradedIntelligenceAccount(ctx, &oldGroup, id, c.Model, c.UpdatedBy)
			}}
			reconciler, ok := any(store).(interface{ ReconcileProtectionScopes(context.Context) error })
			if ok {
				require.NoError(t, reconciler.ReconcileProtectionScopes(ctx))
			}
			after := metric()
			g := groupMetric(after)
			wantBlocked := scenario == "alternative"
			t.Logf("RECONCILED supported_alternative=%v blocked=%v exempt=%v protected=%v allowed=%v", wantBlocked, g.IntelligenceBlocked, g.IntelligenceExempt, intelligenceGroupIDList(account.Extra[intelligenceProtectedGroupsKey]), intelligenceAllowedGroups(account))
			require.True(t, ok, "running controls must reconcile changed protection scopes without another probe")
			require.Equal(t, wantBlocked, g.IntelligenceBlocked)
			require.Equal(t, !wantBlocked, g.IntelligenceExempt)
			require.Contains(t, intelligenceGroupIDList(account.Extra[intelligenceProtectedGroupsKey]), int64(107))
			require.Equal(t, generation, account.GetExtraString(intelligencePauseUntilKey))
			require.Equal(t, before.NormalTotalMS, after.NormalTotalMS)
			require.Equal(t, before.SampleCount, after.SampleCount)
			require.Equal(t, before.IsFastPool, after.IsFastPool)
			require.Zero(t, cache.resetID)
			scheduler := &defaultOpenAIAccountScheduler{service: svc}
			compatible, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{GroupID: &renamed.ID, Platform: PlatformOpenAI, RequestedModel: config.Model, FirstTokenPriority: true})
			require.Equal(t, !wantBlocked, compatible)
			if wantBlocked {
				require.Equal(t, "intelligence_isolated", reason)
			}
			t.Logf("SCHEDULER compatible=%v reason=%q samples=%d fast=%v generation_preserved=true", compatible, reason, after.SampleCount, after.IsFastPool)
			selection, _, err := scheduler.Select(ctx, OpenAIAccountScheduleRequest{GroupID: &renamed.ID, Platform: PlatformOpenAI, RequestedModel: config.Model, FirstTokenPriority: true})
			require.NoError(t, err)
			require.NotNil(t, selection)
			wantID := int64(1)
			if wantBlocked {
				wantID = 2
			}
			require.Equal(t, wantID, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			t.Logf("ACTUAL_SELECT account=%d", selection.Account.ID)
		})
	}
}
