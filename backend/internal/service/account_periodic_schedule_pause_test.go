package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func periodicPauseAccount(anchor time.Time, runMinutes, pauseMinutes int) *Account {
	return &Account{
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			PeriodicSchedulePauseEnabledExtraKey:  true,
			PeriodicScheduleRunMinutesExtraKey:    runMinutes,
			PeriodicSchedulePauseMinutesExtraKey:  pauseMinutes,
			PeriodicSchedulePauseAnchorAtExtraKey: anchor.Format(time.RFC3339Nano),
		},
	}
}

func TestAccountPeriodicSchedulePauseCycle(t *testing.T) {
	anchor := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	account := periodicPauseAccount(anchor, 30, 5)

	tests := []struct {
		name   string
		at     time.Time
		paused bool
	}{
		{name: "starts schedulable", at: anchor, paused: false},
		{name: "still schedulable before boundary", at: anchor.Add(29*time.Minute + 59*time.Second), paused: false},
		{name: "pauses at run boundary", at: anchor.Add(30 * time.Minute), paused: true},
		{name: "stays paused before resume", at: anchor.Add(34*time.Minute + 59*time.Second), paused: true},
		{name: "resumes at cycle boundary", at: anchor.Add(35 * time.Minute), paused: false},
		{name: "pauses in next cycle", at: anchor.Add(65 * time.Minute), paused: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.paused, account.IsInPeriodicSchedulePause(tt.at))
			require.Equal(t, !tt.paused, account.IsSchedulableAt(tt.at))
		})
	}
}

func TestAccountPeriodicSchedulePauseStatus(t *testing.T) {
	anchor := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	account := periodicPauseAccount(anchor, 30, 5)

	running := account.PeriodicSchedulePauseStatusAt(anchor.Add(12 * time.Minute))
	require.True(t, running.Enabled)
	require.False(t, running.Paused)
	require.Equal(t, anchor.Add(30*time.Minute), *running.NextPauseAt)
	require.Nil(t, running.ResumeAt)

	paused := account.PeriodicSchedulePauseStatusAt(anchor.Add(32 * time.Minute))
	require.True(t, paused.Enabled)
	require.True(t, paused.Paused)
	require.Nil(t, paused.NextPauseAt)
	require.Equal(t, anchor.Add(35*time.Minute), *paused.ResumeAt)
}

func TestFilterPeriodicSchedulePausedAccountsRestoresAfterPause(t *testing.T) {
	anchor := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	periodic := periodicPauseAccount(anchor, 30, 5)
	periodic.ID = 1
	healthy := Account{ID: 2, Status: StatusActive, Schedulable: true}
	cached := []Account{*periodic, healthy}

	paused := filterPeriodicSchedulePausedAccounts(cached, anchor.Add(32*time.Minute))
	require.Equal(t, []int64{2}, []int64{paused[0].ID})

	resumed := filterPeriodicSchedulePausedAccounts(cached, anchor.Add(35*time.Minute))
	require.Len(t, resumed, 2)
	require.Equal(t, []int64{1, 2}, []int64{resumed[0].ID, resumed[1].ID})
}

func TestApplyPeriodicSchedulePauseUpdate(t *testing.T) {
	startedAt := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	account := &Account{Extra: map[string]any{}}
	runMinutes, pauseMinutes := 30, 5

	require.NoError(t, applyPeriodicSchedulePauseUpdate(account, &runMinutes, &pauseMinutes, startedAt))
	config, ok := account.GetPeriodicSchedulePauseConfig()
	require.True(t, ok)
	require.Equal(t, runMinutes, config.RunMinutes)
	require.Equal(t, pauseMinutes, config.PauseMinutes)
	require.Equal(t, startedAt, config.AnchorAt)

	unchangedAt := startedAt.Add(10 * time.Minute)
	require.NoError(t, applyPeriodicSchedulePauseUpdate(account, &runMinutes, &pauseMinutes, unchangedAt))
	config, ok = account.GetPeriodicSchedulePauseConfig()
	require.True(t, ok)
	require.Equal(t, startedAt, config.AnchorAt, "saving unchanged values must not restart the cycle")

	runMinutes = 45
	changedAt := startedAt.Add(20 * time.Minute)
	require.NoError(t, applyPeriodicSchedulePauseUpdate(account, &runMinutes, &pauseMinutes, changedAt))
	config, ok = account.GetPeriodicSchedulePauseConfig()
	require.True(t, ok)
	require.Equal(t, changedAt, config.AnchorAt, "changing the cycle restarts it from save time")

	disabled := 0
	require.NoError(t, applyPeriodicSchedulePauseUpdate(account, &disabled, &disabled, changedAt))
	_, ok = account.GetPeriodicSchedulePauseConfig()
	require.False(t, ok)
	require.NotContains(t, account.Extra, PeriodicSchedulePauseEnabledExtraKey)
}

func TestApplyPeriodicSchedulePauseUpdateRejectsInvalidValues(t *testing.T) {
	account := &Account{Extra: map[string]any{}}
	runMinutes, pauseMinutes := 30, 5

	require.Error(t, applyPeriodicSchedulePauseUpdate(account, &runMinutes, nil, time.Now()))
	invalid := -1
	require.Error(t, applyPeriodicSchedulePauseUpdate(account, &invalid, &pauseMinutes, time.Now()))
	tooLarge := MaxPeriodicSchedulePauseWindowMinutes + 1
	require.Error(t, applyPeriodicSchedulePauseUpdate(account, &runMinutes, &tooLarge, time.Now()))
}
