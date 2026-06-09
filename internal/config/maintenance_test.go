package config

import (
	"strings"
	"testing"
	"time"
)

func validManualWindow() MaintenanceWindow {
	return MaintenanceWindow{
		Title:    "Backup window",
		Active:   true,
		JobIDs:   []string{"job-1"},
		Strategy: MaintenanceStrategyManual,
		Timezone: "UTC",
	}
}

func TestUpsertMaintenanceWindowValidationAndUpdate(t *testing.T) {
	windows, saved, err := UpsertMaintenanceWindow(nil, validManualWindow())
	if err != nil {
		t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
	}
	if len(windows) != 1 || saved.ID == "" {
		t.Fatalf("unexpected upsert result: windows=%#v saved=%#v", windows, saved)
	}
	createdAt := saved.CreatedAt
	time.Sleep(1 * time.Millisecond)

	in := validManualWindow()
	in.ID = saved.ID
	in.Title = "Backup window (updated)"
	in.Active = false
	next, updated, err := UpsertMaintenanceWindow(windows, in)
	if err != nil {
		t.Fatalf("UpsertMaintenanceWindow update error = %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("expected one window after update, got %d", len(next))
	}
	if !updated.CreatedAt.Equal(createdAt) {
		t.Fatalf("createdAt changed: before=%v after=%v", createdAt, updated.CreatedAt)
	}
	if updated.Title != "Backup window (updated)" || updated.Active {
		t.Fatalf("update not applied: %#v", updated)
	}
}

func TestDeleteMaintenanceWindow(t *testing.T) {
	windows, saved, err := UpsertMaintenanceWindow(nil, validManualWindow())
	if err != nil {
		t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
	}
	next, removed := DeleteMaintenanceWindow(windows, saved.ID)
	if !removed || len(next) != 0 {
		t.Fatalf("expected window removed, got removed=%v next=%#v", removed, next)
	}

	_, removedAgain := DeleteMaintenanceWindow(next, saved.ID)
	if removedAgain {
		t.Fatal("expected no-op delete for missing ID")
	}
}

func TestNormalizeMaintenanceWindowRequiresTitle(t *testing.T) {
	w := validManualWindow()
	w.Title = "   "
	if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
		t.Fatal("expected error for blank title")
	}
}

func TestNormalizeMaintenanceWindowRequiresTarget(t *testing.T) {
	w := validManualWindow()
	w.JobIDs = nil
	w.DockerHostIDs = nil
	if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
		t.Fatal("expected error when no jobs or hosts are targeted")
	}
}

func TestNormalizeMaintenanceWindowRejectsUnsupportedStrategy(t *testing.T) {
	w := validManualWindow()
	w.Strategy = "bogus"
	if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
		t.Fatal("expected error for unsupported strategy")
	}
}

func TestNormalizeMaintenanceWindowDedupesTargets(t *testing.T) {
	jobsWin := validManualWindow()
	jobsWin.JobIDs = []string{"job-1", "job-1", " job-2", "job-2"}
	jobsWin.DockerHostIDs = nil
	_, saved, err := UpsertMaintenanceWindow(nil, jobsWin)
	if err != nil {
		t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
	}
	if len(saved.JobIDs) != 2 {
		t.Fatalf("expected deduped job targets, got jobIDs=%v", saved.JobIDs)
	}

	hostsWin := validManualWindow()
	hostsWin.JobIDs = nil
	hostsWin.DockerHostIDs = []string{"local", "local"}
	_, saved, err = UpsertMaintenanceWindow(nil, hostsWin)
	if err != nil {
		t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
	}
	if len(saved.DockerHostIDs) != 1 {
		t.Fatalf("expected deduped host targets, got hostIDs=%v", saved.DockerHostIDs)
	}
}

func TestNormalizeMaintenanceWindowRejectsBothJobsAndHosts(t *testing.T) {
	w := validManualWindow()
	w.JobIDs = []string{"job-1"}
	w.DockerHostIDs = []string{"local"}
	if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
		t.Fatal("expected error when both jobs and Docker hosts are targeted")
	}
}

func TestNormalizeMaintenanceWindowOnStart(t *testing.T) {
	t.Run("defaults to allow-finish when blank", func(t *testing.T) {
		w := validManualWindow()
		w.OnStart = ""
		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		if saved.OnStart != MaintenanceOnStartAllowFinish {
			t.Fatalf("OnStart = %q, want %q", saved.OnStart, MaintenanceOnStartAllowFinish)
		}
	})

	t.Run("accepts and lower-cases known values", func(t *testing.T) {
		for _, want := range []string{MaintenanceOnStartAllowFinish, MaintenanceOnStartCancelQueued, MaintenanceOnStartForceCancel} {
			w := validManualWindow()
			w.OnStart = strings.ToUpper(want)
			_, saved, err := UpsertMaintenanceWindow(nil, w)
			if err != nil {
				t.Fatalf("UpsertMaintenanceWindow(%q) error = %v", want, err)
			}
			if saved.OnStart != want {
				t.Fatalf("OnStart = %q, want %q", saved.OnStart, want)
			}
		}
	})

	t.Run("rejects unknown values", func(t *testing.T) {
		w := validManualWindow()
		w.OnStart = "bogus"
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for unsupported onStart behaviour")
		}
	})
}

func TestNormalizeMaintenanceWindowSingle(t *testing.T) {
	base := func() MaintenanceWindow {
		w := validManualWindow()
		w.Strategy = MaintenanceStrategySingle
		return w
	}

	t.Run("requires end after start", func(t *testing.T) {
		w := base()
		start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
		w.Single = &MaintenanceSingleConfig{Start: start, End: start}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error when end is not after start")
		}
	})

	t.Run("accepts valid range and clears unrelated fields", func(t *testing.T) {
		w := base()
		start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
		end := start.Add(2 * time.Hour)
		w.Single = &MaintenanceSingleConfig{Start: start, End: end}
		w.Timezone = "America/New_York"
		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		if saved.Single == nil || !saved.Single.Start.Equal(start) || !saved.Single.End.Equal(end) {
			t.Fatalf("single config not preserved: %#v", saved.Single)
		}
		if saved.Timezone != "" {
			t.Fatalf("expected timezone cleared for single strategy, got %q", saved.Timezone)
		}
	})
}

func TestNormalizeMaintenanceWindowManual(t *testing.T) {
	base := func() MaintenanceWindow {
		w := validManualWindow()
		w.Strategy = MaintenanceStrategyManual
		return w
	}

	t.Run("requires a valid timezone", func(t *testing.T) {
		w := base()
		w.Timezone = ""
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error when timezone is empty")
		}

		w.Timezone = "Not/AZone"
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for an invalid timezone")
		}
	})

	t.Run("accepts an optional effective date range", func(t *testing.T) {
		w := base()
		w.Timezone = "America/New_York"
		from := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
		to := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
		w.EffectiveFrom = &from
		w.EffectiveTo = &to

		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		if saved.Timezone != "America/New_York" {
			t.Fatalf("expected timezone preserved, got %q", saved.Timezone)
		}
		if saved.EffectiveFrom == nil || !saved.EffectiveFrom.Equal(from) || saved.EffectiveTo == nil || !saved.EffectiveTo.Equal(to) {
			t.Fatalf("expected effective range preserved, got from=%v to=%v", saved.EffectiveFrom, saved.EffectiveTo)
		}
	})

	t.Run("works without an effective range", func(t *testing.T) {
		w := base()
		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		if saved.EffectiveFrom != nil || saved.EffectiveTo != nil {
			t.Fatalf("expected no effective range, got from=%v to=%v", saved.EffectiveFrom, saved.EffectiveTo)
		}
	})
}

func TestNormalizeMaintenanceWindowCron(t *testing.T) {
	base := func() MaintenanceWindow {
		w := validManualWindow()
		w.Strategy = MaintenanceStrategyCron
		w.Timezone = "UTC"
		return w
	}

	t.Run("rejects invalid expression", func(t *testing.T) {
		w := base()
		w.Cron = &MaintenanceCronConfig{Expression: "not a cron", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for invalid cron expression")
		}
	})

	t.Run("rejects non-positive duration", func(t *testing.T) {
		w := base()
		w.Cron = &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 0}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for non-positive duration")
		}
	})

	t.Run("requires valid timezone", func(t *testing.T) {
		w := base()
		w.Timezone = "Not/AZone"
		w.Cron = &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for invalid timezone")
		}
	})

	t.Run("accepts valid config", func(t *testing.T) {
		w := base()
		w.Cron = &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 30}
		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		if saved.Cron == nil || saved.Cron.Expression != "0 2 * * *" {
			t.Fatalf("cron config not preserved: %#v", saved.Cron)
		}
	})
}

func TestNormalizeMaintenanceWindowRecurInterval(t *testing.T) {
	base := func() MaintenanceWindow {
		w := validManualWindow()
		w.Strategy = MaintenanceStrategyInterval
		w.Timezone = "UTC"
		return w
	}

	t.Run("requires everyDays >= 1", func(t *testing.T) {
		w := base()
		w.RecurInterval = &MaintenanceRecurInterval{EveryDays: 0, TimeOfDay: "02:00", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for everyDays < 1")
		}
	})

	t.Run("requires valid time of day", func(t *testing.T) {
		w := base()
		w.RecurInterval = &MaintenanceRecurInterval{EveryDays: 1, TimeOfDay: "25:00", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for invalid time of day")
		}
	})

	t.Run("accepts valid config", func(t *testing.T) {
		w := base()
		w.RecurInterval = &MaintenanceRecurInterval{EveryDays: 3, TimeOfDay: "02:00", DurationMinutes: 45}
		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		if saved.RecurInterval == nil || saved.RecurInterval.EveryDays != 3 {
			t.Fatalf("recur-interval config not preserved: %#v", saved.RecurInterval)
		}
	})
}

func TestNormalizeMaintenanceWindowRecurWeekly(t *testing.T) {
	base := func() MaintenanceWindow {
		w := validManualWindow()
		w.Strategy = MaintenanceStrategyWeekly
		w.Timezone = "UTC"
		return w
	}

	t.Run("requires at least one weekday", func(t *testing.T) {
		w := base()
		w.RecurWeekly = &MaintenanceRecurWeekly{Weekdays: nil, TimeOfDay: "02:00", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for empty weekdays")
		}
	})

	t.Run("rejects out-of-range weekday", func(t *testing.T) {
		w := base()
		w.RecurWeekly = &MaintenanceRecurWeekly{Weekdays: []int{7}, TimeOfDay: "02:00", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for out-of-range weekday")
		}
	})

	t.Run("dedupes and sorts weekdays", func(t *testing.T) {
		w := base()
		w.RecurWeekly = &MaintenanceRecurWeekly{Weekdays: []int{3, 1, 1, 5}, TimeOfDay: "02:00", DurationMinutes: 30}
		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		want := []int{1, 3, 5}
		if len(saved.RecurWeekly.Weekdays) != len(want) {
			t.Fatalf("expected %v, got %v", want, saved.RecurWeekly.Weekdays)
		}
		for i, d := range want {
			if saved.RecurWeekly.Weekdays[i] != d {
				t.Fatalf("expected %v, got %v", want, saved.RecurWeekly.Weekdays)
			}
		}
	})
}

func TestNormalizeMaintenanceWindowRecurMonthly(t *testing.T) {
	base := func() MaintenanceWindow {
		w := validManualWindow()
		w.Strategy = MaintenanceStrategyMonthly
		w.Timezone = "UTC"
		return w
	}

	t.Run("requires at least one day of month", func(t *testing.T) {
		w := base()
		w.RecurMonthly = &MaintenanceRecurMonthly{DaysOfMonth: nil, TimeOfDay: "02:00", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for empty days of month")
		}
	})

	t.Run("rejects out-of-range day", func(t *testing.T) {
		w := base()
		w.RecurMonthly = &MaintenanceRecurMonthly{DaysOfMonth: []int{32}, TimeOfDay: "02:00", DurationMinutes: 30}
		if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
			t.Fatal("expected error for out-of-range day")
		}
	})

	t.Run("accepts valid config", func(t *testing.T) {
		w := base()
		w.RecurMonthly = &MaintenanceRecurMonthly{DaysOfMonth: []int{1, 15}, TimeOfDay: "02:00", DurationMinutes: 30}
		_, saved, err := UpsertMaintenanceWindow(nil, w)
		if err != nil {
			t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
		}
		if saved.RecurMonthly == nil || len(saved.RecurMonthly.DaysOfMonth) != 2 {
			t.Fatalf("recur-monthly config not preserved: %#v", saved.RecurMonthly)
		}
	})
}

func TestNormalizeMaintenanceWindowEffectiveRange(t *testing.T) {
	w := validManualWindow()
	w.Strategy = MaintenanceStrategyCron
	w.Timezone = "UTC"
	w.Cron = &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 30}

	from := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	w.EffectiveFrom = &from
	w.EffectiveTo = &to
	if _, _, err := UpsertMaintenanceWindow(nil, w); err == nil {
		t.Fatal("expected error when effective range end is before start")
	}
}

func TestNormalizeMaintenanceWindowClearsOtherStrategyConfigs(t *testing.T) {
	w := validManualWindow()
	w.Strategy = MaintenanceStrategySingle
	start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	w.Single = &MaintenanceSingleConfig{Start: start, End: start.Add(time.Hour)}
	w.Cron = &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 30}
	w.RecurInterval = &MaintenanceRecurInterval{EveryDays: 1, TimeOfDay: "02:00", DurationMinutes: 30}

	_, saved, err := UpsertMaintenanceWindow(nil, w)
	if err != nil {
		t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
	}
	if saved.Cron != nil || saved.RecurInterval != nil || saved.RecurWeekly != nil || saved.RecurMonthly != nil {
		t.Fatalf("expected stale strategy configs cleared: %#v", saved)
	}
}

func TestLoadSaveMaintenanceWindowsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	windows, _, err := UpsertMaintenanceWindow(nil, validManualWindow())
	if err != nil {
		t.Fatalf("UpsertMaintenanceWindow() error = %v", err)
	}
	if err := SaveMaintenanceWindows(dir, windows); err != nil {
		t.Fatalf("SaveMaintenanceWindows() error = %v", err)
	}

	loaded, err := LoadMaintenanceWindows(dir)
	if err != nil {
		t.Fatalf("LoadMaintenanceWindows() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Title != "Backup window" {
		t.Fatalf("unexpected loaded windows: %#v", loaded)
	}
}

func TestLoadMaintenanceWindowsMissingFile(t *testing.T) {
	dir := t.TempDir()
	loaded, err := LoadMaintenanceWindows(dir)
	if err != nil {
		t.Fatalf("LoadMaintenanceWindows() error = %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty slice for missing file, got %#v", loaded)
	}
}

func TestParseTimeOfDay(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
		hour    int
		minute  int
	}{
		{"02:00", false, 2, 0},
		{"23:59", false, 23, 59},
		{"24:00", true, 0, 0},
		{"12:60", true, 0, 0},
		{"not-a-time", true, 0, 0},
		{"1:2:3", true, 0, 0},
	}
	for _, c := range cases {
		hour, minute, err := parseTimeOfDay(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseTimeOfDay(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && (hour != c.hour || minute != c.minute) {
			t.Errorf("parseTimeOfDay(%q) = %d:%d, want %d:%d", c.in, hour, minute, c.hour, c.minute)
		}
	}
}
