package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adhocore/gronx"
	"gopkg.in/yaml.v3"
)

// MaintenanceWindow describes a period during which monitoring is paused for
// selected jobs and/or Docker hosts.
type MaintenanceWindow struct {
	ID          string `yaml:"id" json:"id"`
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Active is the master on/off switch. For the "manual" strategy it IS the
	// toggle; for scheduled strategies it lets a window be paused without
	// being deleted (the schedule only matters while Active is true).
	Active bool `yaml:"active" json:"active"`

	// What this window pauses. A job ID pauses just that job; a Docker host ID
	// pauses every job pinned to that host plus the host's own connectivity
	// monitoring. The built-in "local" host additionally pauses the entire
	// scan cycle.
	JobIDs        []string `yaml:"jobIDs,omitempty" json:"jobIDs,omitempty"`
	DockerHostIDs []string `yaml:"dockerHostIDs,omitempty" json:"dockerHostIDs,omitempty"`

	Strategy string `yaml:"strategy" json:"strategy"`

	// OnStart controls how jobs/actions already queued or in flight against
	// this window's targets are handled the moment the window opens — see
	// the MaintenanceOnStart* constants. Defaults to "allow-finish".
	OnStart string `yaml:"onStart,omitempty" json:"onStart,omitempty"`

	Single        *MaintenanceSingleConfig  `yaml:"single,omitempty" json:"single,omitempty"`
	Cron          *MaintenanceCronConfig    `yaml:"cron,omitempty" json:"cron,omitempty"`
	RecurInterval *MaintenanceRecurInterval `yaml:"recurInterval,omitempty" json:"recurInterval,omitempty"`
	RecurWeekly   *MaintenanceRecurWeekly   `yaml:"recurWeekly,omitempty" json:"recurWeekly,omitempty"`
	RecurMonthly  *MaintenanceRecurMonthly  `yaml:"recurMonthly,omitempty" json:"recurMonthly,omitempty"`

	// Shared by the cron + recurring strategies; left empty for manual & single.
	Timezone      string     `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	EffectiveFrom *time.Time `yaml:"effectiveFrom,omitempty" json:"effectiveFrom,omitempty"`
	EffectiveTo   *time.Time `yaml:"effectiveTo,omitempty" json:"effectiveTo,omitempty"`

	CreatedAt time.Time `yaml:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `yaml:"updatedAt" json:"updatedAt"`
}

// Strategy identifiers.
const (
	MaintenanceStrategyManual    = "manual"
	MaintenanceStrategySingle    = "single"
	MaintenanceStrategyCron      = "cron"
	MaintenanceStrategyInterval  = "recur-interval"
	MaintenanceStrategyWeekly    = "recur-day-of-week"
	MaintenanceStrategyMonthly   = "recur-day-of-month"
)

// OnStart identifiers — how in-flight/queued jobs targeting this window's
// jobs/hosts are handled the moment it opens. See MaintenanceWindow.OnStart.
const (
	// MaintenanceOnStartAllowFinish cancels actions queued for this window's
	// targets that haven't started a Docker call yet, but lets anything
	// already in flight run to completion.
	MaintenanceOnStartAllowFinish = "allow-finish"
	// MaintenanceOnStartForceCancel cancels everything for the window's
	// targets, including Docker calls already in flight, by cancelling the
	// action's context. This can leave a container mid-restart/-stop in an
	// inconsistent state that may need a manual check.
	MaintenanceOnStartForceCancel = "force-cancel"
)

func isSupportedMaintenanceOnStart(v string) bool {
	switch v {
	case MaintenanceOnStartAllowFinish, MaintenanceOnStartForceCancel:
		return true
	default:
		return false
	}
}

// MaintenanceSingleConfig is a one-off start/end timestamp range.
type MaintenanceSingleConfig struct {
	Start time.Time `yaml:"start" json:"start"`
	End   time.Time `yaml:"end" json:"end"`
}

// MaintenanceCronConfig fires on a cron schedule, each occurrence lasting DurationMinutes.
type MaintenanceCronConfig struct {
	Expression      string `yaml:"expression" json:"expression"`
	DurationMinutes int    `yaml:"durationMinutes" json:"durationMinutes"`
}

// MaintenanceRecurInterval fires every EveryDays days at TimeOfDay ("HH:MM").
type MaintenanceRecurInterval struct {
	EveryDays       int    `yaml:"everyDays" json:"everyDays"`
	TimeOfDay       string `yaml:"timeOfDay" json:"timeOfDay"`
	DurationMinutes int    `yaml:"durationMinutes" json:"durationMinutes"`
}

// MaintenanceRecurWeekly fires on the given weekdays (0=Sunday..6=Saturday) at TimeOfDay.
type MaintenanceRecurWeekly struct {
	Weekdays        []int  `yaml:"weekdays" json:"weekdays"`
	TimeOfDay       string `yaml:"timeOfDay" json:"timeOfDay"`
	DurationMinutes int    `yaml:"durationMinutes" json:"durationMinutes"`
}

// MaintenanceRecurMonthly fires on the given days-of-month (1..31) at TimeOfDay.
type MaintenanceRecurMonthly struct {
	DaysOfMonth     []int  `yaml:"daysOfMonth" json:"daysOfMonth"`
	TimeOfDay       string `yaml:"timeOfDay" json:"timeOfDay"`
	DurationMinutes int    `yaml:"durationMinutes" json:"durationMinutes"`
}

const maintenanceConfigVersion = 1

// MaintenanceFile is the on-disk structure of maintenance.yaml.
type MaintenanceFile struct {
	Version int                 `yaml:"version"`
	Windows []MaintenanceWindow `yaml:"windows"`
}

// LoadMaintenanceWindows reads maintenance.yaml and returns the window slice.
func LoadMaintenanceWindows(configDir string) ([]MaintenanceWindow, error) {
	filePath := filepath.Join(configDir, "maintenance.yaml")
	raw, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []MaintenanceWindow{}, nil
		}
		return nil, fmt.Errorf("read maintenance file: %w", err)
	}

	var out MaintenanceFile
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse maintenance yaml: %w", err)
	}
	if out.Windows == nil {
		return []MaintenanceWindow{}, nil
	}
	return out.Windows, nil
}

// SaveMaintenanceWindows normalises and writes windows to maintenance.yaml.
func SaveMaintenanceWindows(configDir string, windows []MaintenanceWindow) error {
	normalized := make([]MaintenanceWindow, 0, len(windows))
	for _, w := range windows {
		n, err := normalizeMaintenanceWindow(w)
		if err != nil {
			return err
		}
		normalized = append(normalized, n)
	}
	return saveMaintenanceVersioned(configDir, normalized)
}

// saveMaintenanceVersioned writes maintenance.yaml with the current schema version,
// skipping the normalize pass (used for already-normalized data).
func saveMaintenanceVersioned(configDir string, windows []MaintenanceWindow) error {
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	sorted := make([]MaintenanceWindow, len(windows))
	copy(sorted, windows)
	sort.SliceStable(sorted, func(i, k int) bool {
		return sorted[i].CreatedAt.Before(sorted[k].CreatedAt)
	})

	payload := MaintenanceFile{Version: maintenanceConfigVersion, Windows: sorted}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal maintenance yaml: %w", err)
	}

	filePath := filepath.Join(configDir, "maintenance.yaml")
	if err := os.WriteFile(filePath, raw, 0o600); err != nil {
		return fmt.Errorf("write maintenance yaml: %w", err)
	}
	return nil
}

// UpsertMaintenanceWindow inserts in if its ID is absent from existing, otherwise updates it.
// Returns the updated slice, the saved window, and any validation error.
func UpsertMaintenanceWindow(existing []MaintenanceWindow, in MaintenanceWindow) ([]MaintenanceWindow, MaintenanceWindow, error) {
	normalized, err := normalizeMaintenanceWindow(in)
	if err != nil {
		return nil, MaintenanceWindow{}, err
	}

	now := time.Now().UTC()
	if normalized.ID == "" {
		normalized.ID = newMaintenanceID(now)
	}

	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now

	for i := range existing {
		if existing[i].ID == normalized.ID {
			normalized.CreatedAt = existing[i].CreatedAt
			existing[i] = normalized
			return existing, normalized, nil
		}
	}

	existing = append(existing, normalized)
	return existing, normalized, nil
}

// DeleteMaintenanceWindow removes the window with the given ID and returns the
// updated slice and whether a window was actually found and removed.
func DeleteMaintenanceWindow(existing []MaintenanceWindow, id string) ([]MaintenanceWindow, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return existing, false
	}

	out := make([]MaintenanceWindow, 0, len(existing))
	removed := false
	for _, w := range existing {
		if w.ID == id {
			removed = true
			continue
		}
		out = append(out, w)
	}
	return out, removed
}

func normalizeMaintenanceWindow(in MaintenanceWindow) (MaintenanceWindow, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Strategy = strings.ToLower(strings.TrimSpace(in.Strategy))
	in.Timezone = strings.TrimSpace(in.Timezone)
	in.OnStart = strings.ToLower(strings.TrimSpace(in.OnStart))
	if in.OnStart == "" || in.OnStart == "cancel-queued" {
		in.OnStart = MaintenanceOnStartAllowFinish
	}

	in.JobIDs = dedupeNonEmpty(in.JobIDs)
	in.DockerHostIDs = dedupeNonEmpty(in.DockerHostIDs)

	if in.Title == "" {
		return MaintenanceWindow{}, fmt.Errorf("maintenance window title is required")
	}
	if !isSupportedMaintenanceStrategy(in.Strategy) {
		return MaintenanceWindow{}, fmt.Errorf("unsupported maintenance strategy %q", in.Strategy)
	}
	if !isSupportedMaintenanceOnStart(in.OnStart) {
		return MaintenanceWindow{}, fmt.Errorf("unsupported onStart behaviour %q", in.OnStart)
	}
	if len(in.JobIDs) == 0 && len(in.DockerHostIDs) == 0 {
		return MaintenanceWindow{}, fmt.Errorf("at least one affected job or Docker host is required")
	}
	if len(in.JobIDs) > 0 && len(in.DockerHostIDs) > 0 {
		return MaintenanceWindow{}, fmt.Errorf("a maintenance window can target either jobs or Docker hosts, not both")
	}

	// Defensive: clear all strategy sub-configs, then re-populate (and validate)
	// only the one that matches — prevents stale data from a prior strategy
	// switch lingering in storage.
	single, cron, interval, weekly, monthly := in.Single, in.Cron, in.RecurInterval, in.RecurWeekly, in.RecurMonthly
	in.Single, in.Cron, in.RecurInterval, in.RecurWeekly, in.RecurMonthly = nil, nil, nil, nil, nil

	switch in.Strategy {
	case MaintenanceStrategyManual:
		if err := requireValidTimezone(in.Timezone); err != nil {
			return MaintenanceWindow{}, err
		}

	case MaintenanceStrategySingle:
		if single == nil {
			return MaintenanceWindow{}, fmt.Errorf("single window requires a start and end time")
		}
		if !single.End.After(single.Start) {
			return MaintenanceWindow{}, fmt.Errorf("single window end must be after start")
		}
		in.Single = single
		in.Timezone = ""
		in.EffectiveFrom = nil
		in.EffectiveTo = nil

	case MaintenanceStrategyCron:
		if cron == nil {
			return MaintenanceWindow{}, fmt.Errorf("cron strategy requires an expression and duration")
		}
		cron.Expression = strings.TrimSpace(cron.Expression)
		if !gronx.IsValid(cron.Expression) {
			return MaintenanceWindow{}, fmt.Errorf("invalid cron expression %q", cron.Expression)
		}
		if cron.DurationMinutes <= 0 {
			return MaintenanceWindow{}, fmt.Errorf("cron strategy requires a positive duration in minutes")
		}
		if err := requireValidTimezone(in.Timezone); err != nil {
			return MaintenanceWindow{}, err
		}
		in.Cron = cron

	case MaintenanceStrategyInterval:
		if interval == nil {
			return MaintenanceWindow{}, fmt.Errorf("recurring-interval strategy requires its fields")
		}
		if interval.EveryDays < 1 {
			return MaintenanceWindow{}, fmt.Errorf("recurring-interval requires \"every N days\" to be at least 1")
		}
		if _, _, err := parseTimeOfDay(interval.TimeOfDay); err != nil {
			return MaintenanceWindow{}, err
		}
		if interval.DurationMinutes <= 0 {
			return MaintenanceWindow{}, fmt.Errorf("recurring-interval requires a positive duration in minutes")
		}
		if err := requireValidTimezone(in.Timezone); err != nil {
			return MaintenanceWindow{}, err
		}
		in.RecurInterval = interval

	case MaintenanceStrategyWeekly:
		if weekly == nil || len(weekly.Weekdays) == 0 {
			return MaintenanceWindow{}, fmt.Errorf("recurring-day-of-week requires at least one weekday")
		}
		days, err := dedupeIntsInRange(weekly.Weekdays, 0, 6, "weekday")
		if err != nil {
			return MaintenanceWindow{}, err
		}
		weekly.Weekdays = days
		if _, _, err := parseTimeOfDay(weekly.TimeOfDay); err != nil {
			return MaintenanceWindow{}, err
		}
		if weekly.DurationMinutes <= 0 {
			return MaintenanceWindow{}, fmt.Errorf("recurring-day-of-week requires a positive duration in minutes")
		}
		if err := requireValidTimezone(in.Timezone); err != nil {
			return MaintenanceWindow{}, err
		}
		in.RecurWeekly = weekly

	case MaintenanceStrategyMonthly:
		if monthly == nil || len(monthly.DaysOfMonth) == 0 {
			return MaintenanceWindow{}, fmt.Errorf("recurring-day-of-month requires at least one day of month")
		}
		days, err := dedupeIntsInRange(monthly.DaysOfMonth, 1, 31, "day of month")
		if err != nil {
			return MaintenanceWindow{}, err
		}
		monthly.DaysOfMonth = days
		if _, _, err := parseTimeOfDay(monthly.TimeOfDay); err != nil {
			return MaintenanceWindow{}, err
		}
		if monthly.DurationMinutes <= 0 {
			return MaintenanceWindow{}, fmt.Errorf("recurring-day-of-month requires a positive duration in minutes")
		}
		if err := requireValidTimezone(in.Timezone); err != nil {
			return MaintenanceWindow{}, err
		}
		in.RecurMonthly = monthly
	}

	if in.EffectiveFrom != nil && in.EffectiveTo != nil && !in.EffectiveTo.After(*in.EffectiveFrom) {
		return MaintenanceWindow{}, fmt.Errorf("effective date range end must be after start")
	}

	return in, nil
}

func isSupportedMaintenanceStrategy(strategy string) bool {
	switch strategy {
	case MaintenanceStrategyManual, MaintenanceStrategySingle, MaintenanceStrategyCron,
		MaintenanceStrategyInterval, MaintenanceStrategyWeekly, MaintenanceStrategyMonthly:
		return true
	default:
		return false
	}
}

func requireValidTimezone(tz string) error {
	if tz == "" {
		return fmt.Errorf("a timezone is required for this strategy")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return nil
}

// parseTimeOfDay parses an "HH:MM" 24-hour time string.
func parseTimeOfDay(s string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time of day %q — expected \"HH:MM\"", s)
	}
	hour, err = strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("invalid time of day %q — hour must be 0-23", s)
	}
	minute, err = strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid time of day %q — minute must be 0-59", s)
	}
	return hour, minute, nil
}

func dedupeNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func dedupeIntsInRange(in []int, min, max int, label string) ([]int, error) {
	out := make([]int, 0, len(in))
	seen := make(map[int]bool, len(in))
	for _, n := range in {
		if n < min || n > max {
			return nil, fmt.Errorf("%s %d is out of range %d-%d", label, n, min, max)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

func newMaintenanceID(t time.Time) string {
	return fmt.Sprintf("maint-%d", t.UnixNano())
}
