package config

import (
	"testing"
	"time"
)

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("time.LoadLocation(%q) error = %v", name, err)
	}
	return loc
}

func TestIsActiveManual(t *testing.T) {
	w := MaintenanceWindow{Strategy: MaintenanceStrategyManual, Active: true}
	if !w.IsActive(time.Now()) {
		t.Fatal("expected manual active=true window to be active at any time")
	}

	w.Active = false
	if w.IsActive(time.Now()) {
		t.Fatal("expected manual active=false window to never be active")
	}
}

func TestIsActiveManualWithEffectiveRange(t *testing.T) {
	from := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	w := MaintenanceWindow{
		Strategy:      MaintenanceStrategyManual,
		Active:        true,
		Timezone:      "UTC",
		EffectiveFrom: &from,
		EffectiveTo:   &to,
	}

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before effective range", time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC), false},
		{"at effective-from (inclusive)", from, true},
		{"inside effective range", time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC), true},
		{"at effective-to (exclusive)", to, false},
		{"after effective range", time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.IsActive(c.t); got != c.want {
				t.Errorf("IsActive(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}

	t.Run("no range set is always active while Active=true", func(t *testing.T) {
		open := MaintenanceWindow{Strategy: MaintenanceStrategyManual, Active: true, Timezone: "UTC"}
		if !open.IsActive(time.Now()) {
			t.Error("expected manual window without an effective range to be active at any time")
		}
	})

	t.Run("inactive when Active=false regardless of range", func(t *testing.T) {
		inactive := w
		inactive.Active = false
		if inactive.IsActive(time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)) {
			t.Error("expected Active=false to short-circuit to inactive even inside the effective range")
		}
	})
}

func TestIsActiveSingle(t *testing.T) {
	start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	w := MaintenanceWindow{
		Strategy: MaintenanceStrategySingle,
		Active:   true,
		Single:   &MaintenanceSingleConfig{Start: start, End: end},
	}

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before", start.Add(-time.Minute), false},
		{"at start (inclusive)", start, true},
		{"inside", start.Add(time.Hour), true},
		{"at end (exclusive)", end, false},
		{"after", end.Add(time.Minute), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.IsActive(c.t); got != c.want {
				t.Errorf("IsActive(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}

	t.Run("inactive when Active=false", func(t *testing.T) {
		inactive := w
		inactive.Active = false
		if inactive.IsActive(start.Add(time.Hour)) {
			t.Error("expected Active=false to short-circuit to inactive")
		}
	})
}

func TestIsActiveCron(t *testing.T) {
	w := MaintenanceWindow{
		Strategy: MaintenanceStrategyCron,
		Active:   true,
		Cron:     &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 60},
		Timezone: "UTC",
	}

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before window", time.Date(2026, 6, 1, 1, 59, 0, 0, time.UTC), false},
		{"at window start", time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC), true},
		{"inside window", time.Date(2026, 6, 1, 2, 30, 0, 0, time.UTC), true},
		{"at window end", time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC), false},
		{"after window", time.Date(2026, 6, 1, 3, 1, 0, 0, time.UTC), false},
		{"next day inside window", time.Date(2026, 6, 2, 2, 15, 0, 0, time.UTC), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.IsActive(c.t); got != c.want {
				t.Errorf("IsActive(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}
}

func TestIsActiveCronAcrossDSTSpringForward(t *testing.T) {
	// America/New_York spring-forward in 2026 is on 2026-03-08, clocks jump
	// 2:00 AM -> 3:00 AM. A "fire at 2:00 AM daily" cron never occurs that day;
	// gronx should resolve to the most recent valid prior occurrence.
	loc := mustLoadLocation(t, "America/New_York")
	w := MaintenanceWindow{
		Strategy: MaintenanceStrategyCron,
		Active:   true,
		Cron:     &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 60},
		Timezone: "America/New_York",
	}

	// 2026-03-09 02:30 local — should be active (today's occurrence).
	withinNextDay := time.Date(2026, 3, 9, 2, 30, 0, 0, loc)
	if !w.IsActive(withinNextDay) {
		t.Errorf("expected active at %v (day after DST jump)", withinNextDay)
	}

	// Far past any nearby occurrence — should be inactive.
	farAfter := time.Date(2026, 3, 9, 10, 0, 0, 0, loc)
	if w.IsActive(farAfter) {
		t.Errorf("expected inactive at %v (long after the window closed)", farAfter)
	}
}

func TestIsActiveCronAcrossDSTFallBack(t *testing.T) {
	// America/New_York fall-back in 2026 is 2026-11-01, clocks go from
	// 2:00 AM back to 1:00 AM (the 1-2 AM hour repeats).
	loc := mustLoadLocation(t, "America/New_York")
	w := MaintenanceWindow{
		Strategy: MaintenanceStrategyCron,
		Active:   true,
		Cron:     &MaintenanceCronConfig{Expression: "0 1 * * *", DurationMinutes: 60},
		Timezone: "America/New_York",
	}

	withinDay := time.Date(2026, 11, 1, 1, 30, 0, 0, loc)
	if !w.IsActive(withinDay) {
		t.Errorf("expected active at %v (fall-back day)", withinDay)
	}

	nextDayInside := time.Date(2026, 11, 2, 1, 15, 0, 0, loc)
	if !w.IsActive(nextDayInside) {
		t.Errorf("expected active at %v (day after fall-back)", nextDayInside)
	}
}

func TestIsActiveRecurInterval(t *testing.T) {
	created := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	w := MaintenanceWindow{
		Strategy:      MaintenanceStrategyInterval,
		Active:        true,
		RecurInterval: &MaintenanceRecurInterval{EveryDays: 2, TimeOfDay: "02:00", DurationMinutes: 30},
		Timezone:      "UTC",
		CreatedAt:     created,
	}

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"on anchor day inside window", time.Date(2026, 6, 1, 2, 15, 0, 0, time.UTC), true},
		{"on anchor day outside window", time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC), false},
		{"on off day (not a multiple of 2)", time.Date(2026, 6, 2, 2, 15, 0, 0, time.UTC), false},
		{"two days later inside window", time.Date(2026, 6, 3, 2, 15, 0, 0, time.UTC), true},
		{"four days later inside window", time.Date(2026, 6, 5, 2, 10, 0, 0, time.UTC), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.IsActive(c.t); got != c.want {
				t.Errorf("IsActive(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}
}

func TestIsActiveRecurWeekly(t *testing.T) {
	// Mon=1, Wed=3
	w := MaintenanceWindow{
		Strategy:    MaintenanceStrategyWeekly,
		Active:      true,
		RecurWeekly: &MaintenanceRecurWeekly{Weekdays: []int{1, 3}, TimeOfDay: "02:00", DurationMinutes: 60},
		Timezone:    "UTC",
	}

	// 2026-06-01 is a Monday.
	monday := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	wednesday := monday.AddDate(0, 0, 2)
	tuesday := monday.AddDate(0, 0, 1)

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"monday inside window", monday.Add(2*time.Hour + 30*time.Minute), true},
		{"monday outside window", monday.Add(4 * time.Hour), false},
		{"tuesday (not selected)", tuesday.Add(2*time.Hour + 15*time.Minute), false},
		{"wednesday inside window", wednesday.Add(2*time.Hour + 45*time.Minute), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.IsActive(c.t); got != c.want {
				t.Errorf("IsActive(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}
}

func TestIsActiveRecurMonthly(t *testing.T) {
	w := MaintenanceWindow{
		Strategy:     MaintenanceStrategyMonthly,
		Active:       true,
		RecurMonthly: &MaintenanceRecurMonthly{DaysOfMonth: []int{1, 15}, TimeOfDay: "02:00", DurationMinutes: 60},
		Timezone:     "UTC",
	}

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"1st inside window", time.Date(2026, 6, 1, 2, 30, 0, 0, time.UTC), true},
		{"1st outside window", time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC), false},
		{"2nd (not selected)", time.Date(2026, 6, 2, 2, 30, 0, 0, time.UTC), false},
		{"15th inside window", time.Date(2026, 6, 15, 2, 45, 0, 0, time.UTC), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.IsActive(c.t); got != c.want {
				t.Errorf("IsActive(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}
}

func TestIsActiveEffectiveRangeBounding(t *testing.T) {
	from := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	w := MaintenanceWindow{
		Strategy:     MaintenanceStrategyMonthly,
		Active:       true,
		RecurMonthly: &MaintenanceRecurMonthly{DaysOfMonth: []int{1, 10, 25}, TimeOfDay: "02:00", DurationMinutes: 60},
		Timezone:     "UTC",
		EffectiveFrom: &from,
		EffectiveTo:   &to,
	}

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"would-fire but before effective range", time.Date(2026, 6, 1, 2, 30, 0, 0, time.UTC), false},
		{"fires inside effective range", time.Date(2026, 6, 10, 2, 30, 0, 0, time.UTC), true},
		{"would-fire but after effective range", time.Date(2026, 6, 25, 2, 30, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.IsActive(c.t); got != c.want {
				t.Errorf("IsActive(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}
}

func TestIsActiveShortCircuitsOnInactive(t *testing.T) {
	now := time.Now()
	cron := time.Now().Format("15:04")

	windows := []MaintenanceWindow{
		{Strategy: MaintenanceStrategyManual, Active: false},
		{Strategy: MaintenanceStrategySingle, Active: false, Single: &MaintenanceSingleConfig{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}},
		{Strategy: MaintenanceStrategyCron, Active: false, Timezone: "UTC", Cron: &MaintenanceCronConfig{Expression: "* * * * *", DurationMinutes: 60}},
		{Strategy: MaintenanceStrategyInterval, Active: false, Timezone: "UTC", RecurInterval: &MaintenanceRecurInterval{EveryDays: 1, TimeOfDay: cron, DurationMinutes: 60}},
		{Strategy: MaintenanceStrategyWeekly, Active: false, Timezone: "UTC", RecurWeekly: &MaintenanceRecurWeekly{Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, TimeOfDay: cron, DurationMinutes: 60}},
		{Strategy: MaintenanceStrategyMonthly, Active: false, Timezone: "UTC", RecurMonthly: &MaintenanceRecurMonthly{DaysOfMonth: []int{now.Day()}, TimeOfDay: cron, DurationMinutes: 60}},
	}

	for i, w := range windows {
		if w.IsActive(now) {
			t.Errorf("window[%d] strategy=%s: expected Active=false to short-circuit to inactive", i, w.Strategy)
		}
	}

	if active := ActiveMaintenanceWindows(windows, now); len(active) != 0 {
		t.Errorf("expected no active windows, got %d", len(active))
	}
}

func TestActiveMaintenanceWindowsFiltersCorrectly(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	windows := []MaintenanceWindow{
		{ID: "a", Strategy: MaintenanceStrategyManual, Active: true},
		{ID: "b", Strategy: MaintenanceStrategyManual, Active: false},
		{ID: "c", Strategy: MaintenanceStrategySingle, Active: true, Single: &MaintenanceSingleConfig{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}},
		{ID: "d", Strategy: MaintenanceStrategySingle, Active: true, Single: &MaintenanceSingleConfig{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)}},
	}

	active := ActiveMaintenanceWindows(windows, now)
	if len(active) != 2 {
		t.Fatalf("expected 2 active windows, got %d: %#v", len(active), active)
	}
	ids := map[string]bool{active[0].ID: true, active[1].ID: true}
	if !ids["a"] || !ids["c"] {
		t.Fatalf("expected windows a and c active, got %v", ids)
	}
}

func TestNextOccurrence(t *testing.T) {
	t.Run("manual", func(t *testing.T) {
		future := time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC)
		ref := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

		w := MaintenanceWindow{Strategy: MaintenanceStrategyManual, Active: true, Timezone: "UTC", EffectiveFrom: &future}
		got, ok := w.NextOccurrence(ref)
		if !ok || !got.Equal(future) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", ref, got, ok, future)
		}

		if _, ok := w.NextOccurrence(future.Add(time.Hour)); ok {
			t.Error("expected no next occurrence once the effective-from date has passed")
		}

		open := MaintenanceWindow{Strategy: MaintenanceStrategyManual, Active: true, Timezone: "UTC"}
		if _, ok := open.NextOccurrence(ref); ok {
			t.Error("expected no deterministic next occurrence for an open-ended manual window")
		}

		inactive := w
		inactive.Active = false
		if _, ok := inactive.NextOccurrence(ref); ok {
			t.Error("expected inactive window to report no next occurrence")
		}
	})

	t.Run("single", func(t *testing.T) {
		start := time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC)
		end := start.Add(2 * time.Hour)
		w := MaintenanceWindow{Strategy: MaintenanceStrategySingle, Active: true, Single: &MaintenanceSingleConfig{Start: start, End: end}}

		ref := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		got, ok := w.NextOccurrence(ref)
		if !ok || !got.Equal(start) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", ref, got, ok, start)
		}

		if _, ok := w.NextOccurrence(start.Add(time.Hour)); ok {
			t.Error("expected no next occurrence once the single window has started")
		}
	})

	t.Run("cron", func(t *testing.T) {
		w := MaintenanceWindow{
			Strategy: MaintenanceStrategyCron,
			Active:   true,
			Cron:     &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 60},
			Timezone: "UTC",
		}

		// Before today's tick — the next occurrence is later today.
		beforeTick := time.Date(2026, 6, 1, 0, 30, 0, 0, time.UTC)
		wantToday := time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(beforeTick); !ok || !got.Equal(wantToday) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", beforeTick, got, ok, wantToday)
		}

		// After today's tick — the next occurrence rolls over to tomorrow.
		afterTick := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
		wantTomorrow := time.Date(2026, 6, 2, 2, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(afterTick); !ok || !got.Equal(wantTomorrow) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", afterTick, got, ok, wantTomorrow)
		}
	})

	t.Run("recurring interval", func(t *testing.T) {
		created := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		w := MaintenanceWindow{
			Strategy:      MaintenanceStrategyInterval,
			Active:        true,
			RecurInterval: &MaintenanceRecurInterval{EveryDays: 2, TimeOfDay: "02:00", DurationMinutes: 30},
			Timezone:      "UTC",
			CreatedAt:     created,
		}

		// Before the anchor day's tick — that's the next occurrence.
		beforeTick := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		wantAnchor := time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(beforeTick); !ok || !got.Equal(wantAnchor) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", beforeTick, got, ok, wantAnchor)
		}

		// After the anchor day's tick — rolls over to the next aligned day (anchor + 2 days).
		afterTick := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
		wantNextAligned := time.Date(2026, 6, 3, 2, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(afterTick); !ok || !got.Equal(wantNextAligned) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", afterTick, got, ok, wantNextAligned)
		}
	})

	t.Run("recurring weekly", func(t *testing.T) {
		// Mon=1, Wed=3. 2026-06-01 is a Monday.
		w := MaintenanceWindow{
			Strategy:    MaintenanceStrategyWeekly,
			Active:      true,
			RecurWeekly: &MaintenanceRecurWeekly{Weekdays: []int{1, 3}, TimeOfDay: "02:00", DurationMinutes: 60},
			Timezone:    "UTC",
		}
		monday := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

		wantToday := time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(monday); !ok || !got.Equal(wantToday) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", monday, got, ok, wantToday)
		}

		afterMondayTick := monday.Add(3 * time.Hour)
		wantWednesday := time.Date(2026, 6, 3, 2, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(afterMondayTick); !ok || !got.Equal(wantWednesday) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", afterMondayTick, got, ok, wantWednesday)
		}
	})

	t.Run("recurring monthly", func(t *testing.T) {
		w := MaintenanceWindow{
			Strategy:     MaintenanceStrategyMonthly,
			Active:       true,
			RecurMonthly: &MaintenanceRecurMonthly{DaysOfMonth: []int{1, 15}, TimeOfDay: "03:00", DurationMinutes: 60},
			Timezone:     "UTC",
		}

		beforeTick := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		wantFirst := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(beforeTick); !ok || !got.Equal(wantFirst) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", beforeTick, got, ok, wantFirst)
		}

		afterFirstTick := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)
		wantFifteenth := time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(afterFirstTick); !ok || !got.Equal(wantFifteenth) {
			t.Errorf("NextOccurrence(%v) = %v, %v; want %v, true", afterFirstTick, got, ok, wantFifteenth)
		}
	})

	t.Run("recurring window outside an upcoming effective range reports none", func(t *testing.T) {
		from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
		w := MaintenanceWindow{
			Strategy:      MaintenanceStrategyCron,
			Active:        true,
			Cron:          &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 60},
			Timezone:      "UTC",
			EffectiveFrom: &from,
			EffectiveTo:   &to,
		}

		// The very next tick (June 2, 2 AM) falls before the effective range
		// opens (July 1) — NextOccurrence reports "none" rather than searching
		// further forward (a deliberate simplification for a best-effort
		// reminder feature, not a pause-correctness decision).
		ref := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
		if got, ok := w.NextOccurrence(ref); ok {
			t.Errorf("NextOccurrence(%v) = %v, true; want ok=false (next tick falls outside the upcoming effective range)", ref, got)
		}
	})

	t.Run("inactive windows report no next occurrence", func(t *testing.T) {
		now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		windows := []MaintenanceWindow{
			{Strategy: MaintenanceStrategyManual, Active: false, Timezone: "UTC"},
			{Strategy: MaintenanceStrategySingle, Active: false, Single: &MaintenanceSingleConfig{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)}},
			{Strategy: MaintenanceStrategyCron, Active: false, Timezone: "UTC", Cron: &MaintenanceCronConfig{Expression: "* * * * *", DurationMinutes: 60}},
		}
		for i, w := range windows {
			if _, ok := w.NextOccurrence(now); ok {
				t.Errorf("window[%d] strategy=%s: expected inactive window to report no next occurrence", i, w.Strategy)
			}
		}
	})
}

func TestLastOccurrence(t *testing.T) {
	t.Run("manual", func(t *testing.T) {
		past := time.Date(2026, 5, 20, 8, 0, 0, 0, time.UTC)
		ref := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

		w := MaintenanceWindow{Strategy: MaintenanceStrategyManual, Active: true, Timezone: "UTC", EffectiveFrom: &past}
		got, ok := w.LastOccurrence(ref)
		if !ok || !got.Equal(past) {
			t.Errorf("LastOccurrence(%v) = %v, %v; want %v, true", ref, got, ok, past)
		}

		if _, ok := w.LastOccurrence(past.Add(-time.Hour)); ok {
			t.Error("expected no last occurrence before the effective-from date")
		}

		open := MaintenanceWindow{Strategy: MaintenanceStrategyManual, Active: true, Timezone: "UTC"}
		if _, ok := open.LastOccurrence(ref); ok {
			t.Error("expected no deterministic last occurrence for an open-ended manual window")
		}

		// Unlike NextOccurrence, LastOccurrence is NOT gated on Active — "when did
		// this last run" stays meaningful even for a currently-disabled window.
		inactive := w
		inactive.Active = false
		got, ok = inactive.LastOccurrence(ref)
		if !ok || !got.Equal(past) {
			t.Errorf("LastOccurrence(%v) on inactive window = %v, %v; want %v, true", ref, got, ok, past)
		}
	})

	t.Run("single", func(t *testing.T) {
		start := time.Date(2026, 5, 20, 8, 0, 0, 0, time.UTC)
		end := start.Add(2 * time.Hour)
		w := MaintenanceWindow{Strategy: MaintenanceStrategySingle, Active: true, Single: &MaintenanceSingleConfig{Start: start, End: end}}

		ref := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		got, ok := w.LastOccurrence(ref)
		if !ok || !got.Equal(start) {
			t.Errorf("LastOccurrence(%v) = %v, %v; want %v, true", ref, got, ok, start)
		}

		if _, ok := w.LastOccurrence(start.Add(-time.Hour)); ok {
			t.Error("expected no last occurrence before the single window has started")
		}
	})

	t.Run("cron", func(t *testing.T) {
		w := MaintenanceWindow{
			Strategy: MaintenanceStrategyCron,
			Active:   true,
			Cron:     &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 60},
			Timezone: "UTC",
		}

		// After today's tick — the most recent occurrence is today's.
		afterTick := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
		wantToday := time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC)
		if got, ok := w.LastOccurrence(afterTick); !ok || !got.Equal(wantToday) {
			t.Errorf("LastOccurrence(%v) = %v, %v; want %v, true", afterTick, got, ok, wantToday)
		}

		// Before today's tick — the most recent occurrence rolls back to yesterday.
		beforeTick := time.Date(2026, 6, 1, 0, 30, 0, 0, time.UTC)
		wantYesterday := time.Date(2026, 5, 31, 2, 0, 0, 0, time.UTC)
		if got, ok := w.LastOccurrence(beforeTick); !ok || !got.Equal(wantYesterday) {
			t.Errorf("LastOccurrence(%v) = %v, %v; want %v, true", beforeTick, got, ok, wantYesterday)
		}
	})

	t.Run("recurring weekly", func(t *testing.T) {
		// Mon=1, Wed=3. 2026-06-03 is a Wednesday.
		w := MaintenanceWindow{
			Strategy:    MaintenanceStrategyWeekly,
			Active:      true,
			RecurWeekly: &MaintenanceRecurWeekly{Weekdays: []int{1, 3}, TimeOfDay: "02:00", DurationMinutes: 60},
			Timezone:    "UTC",
		}

		afterWedTick := time.Date(2026, 6, 3, 5, 0, 0, 0, time.UTC)
		wantWednesday := time.Date(2026, 6, 3, 2, 0, 0, 0, time.UTC)
		if got, ok := w.LastOccurrence(afterWedTick); !ok || !got.Equal(wantWednesday) {
			t.Errorf("LastOccurrence(%v) = %v, %v; want %v, true", afterWedTick, got, ok, wantWednesday)
		}

		beforeWedTick := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
		wantMonday := time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC)
		if got, ok := w.LastOccurrence(beforeWedTick); !ok || !got.Equal(wantMonday) {
			t.Errorf("LastOccurrence(%v) = %v, %v; want %v, true", beforeWedTick, got, ok, wantMonday)
		}
	})

	t.Run("disabled windows still report a last occurrence", func(t *testing.T) {
		now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		past := now.Add(-24 * time.Hour)
		windows := []MaintenanceWindow{
			{Strategy: MaintenanceStrategyManual, Active: false, Timezone: "UTC", EffectiveFrom: &past},
			{Strategy: MaintenanceStrategySingle, Active: false, Single: &MaintenanceSingleConfig{Start: past, End: past.Add(time.Hour)}},
			{Strategy: MaintenanceStrategyCron, Active: false, Timezone: "UTC", Cron: &MaintenanceCronConfig{Expression: "0 2 * * *", DurationMinutes: 60}},
		}
		for i, w := range windows {
			if _, ok := w.LastOccurrence(now); !ok {
				t.Errorf("window[%d] strategy=%s: expected a last occurrence even though the window is disabled", i, w.Strategy)
			}
		}
	})

	t.Run("windows with no occurrence yet report none", func(t *testing.T) {
		future := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		ref := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		windows := []MaintenanceWindow{
			{Strategy: MaintenanceStrategyManual, Active: true, Timezone: "UTC", EffectiveFrom: &future},
			{Strategy: MaintenanceStrategySingle, Active: true, Single: &MaintenanceSingleConfig{Start: future, End: future.Add(time.Hour)}},
		}
		for i, w := range windows {
			if _, ok := w.LastOccurrence(ref); ok {
				t.Errorf("window[%d] strategy=%s: expected no last occurrence before the window's first run", i, w.Strategy)
			}
		}
	})
}
