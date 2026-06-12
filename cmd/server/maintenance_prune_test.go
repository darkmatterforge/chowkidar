package main

import (
	"testing"
	"time"

	"chowkidar/internal/config"
)

func TestShouldAutoRemoveMaintenanceWindow(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	finishedLongAgo := now.Add(-13 * time.Hour)
	finishedRecently := now.Add(-1 * time.Hour)
	stillRunning := now.Add(1 * time.Hour)

	cases := []struct {
		name string
		w    config.MaintenanceWindow
		want bool
	}{
		{
			name: "single window finished more than 12h ago is removed",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategySingle, Single: &config.MaintenanceSingleConfig{Start: finishedLongAgo.Add(-time.Hour), End: finishedLongAgo}},
			want: true,
		},
		{
			name: "single window finished less than 12h ago is kept",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategySingle, Single: &config.MaintenanceSingleConfig{Start: finishedRecently.Add(-time.Hour), End: finishedRecently}},
			want: false,
		},
		{
			name: "single window still running is kept",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategySingle, Single: &config.MaintenanceSingleConfig{Start: now.Add(-time.Hour), End: stillRunning}},
			want: false,
		},
		{
			name: "manual window with effective-to more than 12h in the past is removed",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategyManual, EffectiveTo: timePtr(finishedLongAgo)},
			want: true,
		},
		{
			name: "manual window with effective-to less than 12h in the past is kept",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategyManual, EffectiveTo: timePtr(finishedRecently)},
			want: false,
		},
		{
			name: "open-ended manual window without effective-to is kept indefinitely",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategyManual},
			want: false,
		},
		{
			name: "cron window is kept regardless of a stale effective-to",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategyCron, EffectiveTo: timePtr(finishedLongAgo)},
			want: false,
		},
		{
			name: "recurring interval window is kept regardless of a stale effective-to",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategyInterval, EffectiveTo: timePtr(finishedLongAgo)},
			want: false,
		},
		{
			name: "recurring weekly window is kept regardless of a stale effective-to",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategyWeekly, EffectiveTo: timePtr(finishedLongAgo)},
			want: false,
		},
		{
			name: "recurring monthly window is kept regardless of a stale effective-to",
			w:    config.MaintenanceWindow{Strategy: config.MaintenanceStrategyMonthly, EffectiveTo: timePtr(finishedLongAgo)},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAutoRemoveMaintenanceWindow(tc.w, now); got != tc.want {
				t.Errorf("shouldAutoRemoveMaintenanceWindow() = %v, want %v", got, tc.want)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}
