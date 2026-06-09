package config

import (
	"time"

	"github.com/adhocore/gronx"
)

// IsActive reports whether the window is currently pausing monitoring at time t.
func (w MaintenanceWindow) IsActive(t time.Time) bool {
	if !w.Active {
		return false
	}

	switch w.Strategy {
	case MaintenanceStrategyManual:
		loc, err := time.LoadLocation(w.Timezone)
		if err != nil {
			loc = time.UTC
		}
		return withinEffectiveRange(w.EffectiveFrom, w.EffectiveTo, t.In(loc))

	case MaintenanceStrategySingle:
		if w.Single == nil {
			return false
		}
		return !t.Before(w.Single.Start) && t.Before(w.Single.End)

	case MaintenanceStrategyCron, MaintenanceStrategyInterval, MaintenanceStrategyWeekly, MaintenanceStrategyMonthly:
		return w.isRecurringActive(t)

	default:
		return false
	}
}

// ActiveMaintenanceWindows filters windows to those currently active at time t.
// This is the single source of truth used by both pause evaluation and the
// dashboard "active windows" endpoint.
func ActiveMaintenanceWindows(windows []MaintenanceWindow, t time.Time) []MaintenanceWindow {
	active := make([]MaintenanceWindow, 0, len(windows))
	for _, w := range windows {
		if w.IsActive(t) {
			active = append(active, w)
		}
	}
	return active
}

// NextOccurrence returns the start time of the next scheduled occurrence
// strictly after t, and whether one could be determined. It is the
// forward-looking counterpart to mostRecentOccurrence/IsActive, used to power
// "upcoming maintenance" reminders rather than pause-correctness decisions —
// so unlike isRecurringActive, if the raw next tick falls outside an upcoming
// effective date range it simply reports "none" rather than searching further.
func (w MaintenanceWindow) NextOccurrence(t time.Time) (time.Time, bool) {
	if !w.Active {
		return time.Time{}, false
	}

	switch w.Strategy {
	case MaintenanceStrategyManual:
		if w.EffectiveFrom == nil || !w.EffectiveFrom.After(t) {
			return time.Time{}, false
		}
		return *w.EffectiveFrom, true

	case MaintenanceStrategySingle:
		if w.Single == nil || !w.Single.Start.After(t) {
			return time.Time{}, false
		}
		return w.Single.Start, true

	case MaintenanceStrategyCron, MaintenanceStrategyInterval, MaintenanceStrategyWeekly, MaintenanceStrategyMonthly:
		loc, err := time.LoadLocation(w.Timezone)
		if err != nil {
			loc = time.UTC
		}
		start, ok := w.firstOccurrenceAfter(t.In(loc), loc)
		if !ok || !withinEffectiveRange(w.EffectiveFrom, w.EffectiveTo, start) {
			return time.Time{}, false
		}
		return start, true

	default:
		return time.Time{}, false
	}
}

// LastOccurrence returns the start time of the most recent scheduled
// occurrence at or before t, and whether one could be determined. It is the
// backward-looking sibling of NextOccurrence, used to show "last ran" in the
// windows list. Unlike NextOccurrence (and IsActive), it is deliberately NOT
// gated on w.Active — "when did this last run" stays meaningful presentational
// info even for a currently-disabled window. This is a display helper, not
// pause-correctness logic.
func (w MaintenanceWindow) LastOccurrence(t time.Time) (time.Time, bool) {
	switch w.Strategy {
	case MaintenanceStrategyManual:
		if w.EffectiveFrom == nil || w.EffectiveFrom.After(t) {
			return time.Time{}, false
		}
		return *w.EffectiveFrom, true

	case MaintenanceStrategySingle:
		if w.Single == nil || w.Single.Start.After(t) {
			return time.Time{}, false
		}
		return w.Single.Start, true

	case MaintenanceStrategyCron, MaintenanceStrategyInterval, MaintenanceStrategyWeekly, MaintenanceStrategyMonthly:
		loc, err := time.LoadLocation(w.Timezone)
		if err != nil {
			loc = time.UTC
		}
		start, _, ok := w.mostRecentOccurrence(t.In(loc), loc)
		if !ok {
			return time.Time{}, false
		}
		return start, true

	default:
		return time.Time{}, false
	}
}

// isRecurringActive funnels the cron + 3 recurring strategies through one
// "find the most recent occurrence, check now falls within [start, start+duration)"
// predicate, after first checking the optional effective date range.
func (w MaintenanceWindow) isRecurringActive(t time.Time) bool {
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := t.In(loc)

	if !withinEffectiveRange(w.EffectiveFrom, w.EffectiveTo, local) {
		return false
	}

	occStart, duration, ok := w.mostRecentOccurrence(local, loc)
	if !ok {
		return false
	}
	return !local.Before(occStart) && local.Before(occStart.Add(duration))
}

// mostRecentOccurrence returns the start time and duration of the most recent
// scheduled occurrence at or before ref (in loc), and whether one was found.
func (w MaintenanceWindow) mostRecentOccurrence(ref time.Time, loc *time.Location) (time.Time, time.Duration, bool) {
	switch w.Strategy {
	case MaintenanceStrategyCron:
		if w.Cron == nil {
			return time.Time{}, 0, false
		}
		start, err := gronx.PrevTickBefore(w.Cron.Expression, ref, true)
		if err != nil {
			return time.Time{}, 0, false
		}
		return start.In(loc), time.Duration(w.Cron.DurationMinutes) * time.Minute, true

	case MaintenanceStrategyInterval:
		if w.RecurInterval == nil || w.RecurInterval.EveryDays < 1 {
			return time.Time{}, 0, false
		}
		hour, minute, err := parseTimeOfDay(w.RecurInterval.TimeOfDay)
		if err != nil {
			return time.Time{}, 0, false
		}
		duration := time.Duration(w.RecurInterval.DurationMinutes) * time.Minute

		anchor := w.CreatedAt
		if anchor.IsZero() {
			anchor = ref
		}
		anchor = anchor.In(loc)
		anchorDay := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), hour, minute, 0, 0, loc)

		// Start from today's occurrence time, stepping back a day if it
		// hasn't happened yet today, then walk backward looking for the
		// nearest anchor-aligned day. At most EveryDays steps are needed
		// since alignment repeats with that period.
		start := time.Date(ref.Year(), ref.Month(), ref.Day(), hour, minute, 0, 0, loc)
		if start.After(ref) {
			start = start.AddDate(0, 0, -1)
		}
		if start.Before(anchorDay) {
			return time.Time{}, 0, false
		}

		for i := 0; i < w.RecurInterval.EveryDays; i++ {
			candidate := start.AddDate(0, 0, -i)
			if candidate.Before(anchorDay) {
				break
			}
			if daysBetweenDates(anchorDay, candidate)%w.RecurInterval.EveryDays == 0 {
				return candidate, duration, true
			}
		}
		return time.Time{}, 0, false

	case MaintenanceStrategyWeekly:
		if w.RecurWeekly == nil || len(w.RecurWeekly.Weekdays) == 0 {
			return time.Time{}, 0, false
		}
		hour, minute, err := parseTimeOfDay(w.RecurWeekly.TimeOfDay)
		if err != nil {
			return time.Time{}, 0, false
		}
		duration := time.Duration(w.RecurWeekly.DurationMinutes) * time.Minute
		wanted := make(map[int]bool, len(w.RecurWeekly.Weekdays))
		for _, d := range w.RecurWeekly.Weekdays {
			wanted[d] = true
		}

		for i := 0; i < 8; i++ {
			day := ref.AddDate(0, 0, -i)
			if !wanted[int(day.Weekday())] {
				continue
			}
			candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
			if !candidate.After(ref) {
				return candidate, duration, true
			}
		}
		return time.Time{}, 0, false

	case MaintenanceStrategyMonthly:
		if w.RecurMonthly == nil || len(w.RecurMonthly.DaysOfMonth) == 0 {
			return time.Time{}, 0, false
		}
		hour, minute, err := parseTimeOfDay(w.RecurMonthly.TimeOfDay)
		if err != nil {
			return time.Time{}, 0, false
		}
		duration := time.Duration(w.RecurMonthly.DurationMinutes) * time.Minute
		wanted := make(map[int]bool, len(w.RecurMonthly.DaysOfMonth))
		for _, d := range w.RecurMonthly.DaysOfMonth {
			wanted[d] = true
		}

		for i := 0; i < 32; i++ {
			day := ref.AddDate(0, 0, -i)
			if !wanted[day.Day()] {
				continue
			}
			candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
			if !candidate.After(ref) {
				return candidate, duration, true
			}
		}
		return time.Time{}, 0, false

	default:
		return time.Time{}, 0, false
	}
}

// firstOccurrenceAfter returns the start time of the first scheduled
// occurrence strictly after ref (in loc), and whether one was found. It is
// the forward-scanning mirror of mostRecentOccurrence, used by NextOccurrence
// for upcoming-maintenance reminders.
func (w MaintenanceWindow) firstOccurrenceAfter(ref time.Time, loc *time.Location) (time.Time, bool) {
	switch w.Strategy {
	case MaintenanceStrategyCron:
		if w.Cron == nil {
			return time.Time{}, false
		}
		start, err := gronx.NextTickAfter(w.Cron.Expression, ref, false)
		if err != nil {
			return time.Time{}, false
		}
		return start.In(loc), true

	case MaintenanceStrategyInterval:
		if w.RecurInterval == nil || w.RecurInterval.EveryDays < 1 {
			return time.Time{}, false
		}
		hour, minute, err := parseTimeOfDay(w.RecurInterval.TimeOfDay)
		if err != nil {
			return time.Time{}, false
		}

		anchor := w.CreatedAt
		if anchor.IsZero() {
			anchor = ref
		}
		anchor = anchor.In(loc)
		anchorDay := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), hour, minute, 0, 0, loc)

		// Start from today's occurrence time, stepping forward a day if it has
		// already passed, then walk forward looking for the nearest
		// anchor-aligned day. At most EveryDays steps are needed since
		// alignment repeats with that period.
		start := time.Date(ref.Year(), ref.Month(), ref.Day(), hour, minute, 0, 0, loc)
		if !start.After(ref) {
			start = start.AddDate(0, 0, 1)
		}
		if start.Before(anchorDay) {
			start = anchorDay
		}

		for i := 0; i < w.RecurInterval.EveryDays; i++ {
			candidate := start.AddDate(0, 0, i)
			if daysBetweenDates(anchorDay, candidate)%w.RecurInterval.EveryDays == 0 {
				return candidate, true
			}
		}
		return time.Time{}, false

	case MaintenanceStrategyWeekly:
		if w.RecurWeekly == nil || len(w.RecurWeekly.Weekdays) == 0 {
			return time.Time{}, false
		}
		hour, minute, err := parseTimeOfDay(w.RecurWeekly.TimeOfDay)
		if err != nil {
			return time.Time{}, false
		}
		wanted := make(map[int]bool, len(w.RecurWeekly.Weekdays))
		for _, d := range w.RecurWeekly.Weekdays {
			wanted[d] = true
		}

		for i := 0; i < 8; i++ {
			day := ref.AddDate(0, 0, i)
			if !wanted[int(day.Weekday())] {
				continue
			}
			candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
			if candidate.After(ref) {
				return candidate, true
			}
		}
		return time.Time{}, false

	case MaintenanceStrategyMonthly:
		if w.RecurMonthly == nil || len(w.RecurMonthly.DaysOfMonth) == 0 {
			return time.Time{}, false
		}
		hour, minute, err := parseTimeOfDay(w.RecurMonthly.TimeOfDay)
		if err != nil {
			return time.Time{}, false
		}
		wanted := make(map[int]bool, len(w.RecurMonthly.DaysOfMonth))
		for _, d := range w.RecurMonthly.DaysOfMonth {
			wanted[d] = true
		}

		for i := 0; i < 32; i++ {
			day := ref.AddDate(0, 0, i)
			if !wanted[day.Day()] {
				continue
			}
			candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
			if candidate.After(ref) {
				return candidate, true
			}
		}
		return time.Time{}, false

	default:
		return time.Time{}, false
	}
}

func withinEffectiveRange(from, to *time.Time, ref time.Time) bool {
	if from != nil && ref.Before(*from) {
		return false
	}
	if to != nil && !ref.Before(*to) {
		return false
	}
	return true
}

// daysBetweenDates returns the number of calendar days between a and b
// (b - a), independent of their times-of-day or zones. Reconstructing both
// in UTC at midnight avoids DST-induced fractional-day drift that a raw
// duration division would suffer from.
func daysBetweenDates(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	au := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	bu := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(bu.Sub(au).Hours() / 24)
}
