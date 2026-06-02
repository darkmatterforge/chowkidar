package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Job struct {
	ID                           string    `yaml:"id" json:"id"`
	Name                         string    `yaml:"name" json:"name"`
	Group                        string    `yaml:"group,omitempty" json:"group,omitempty"`
	Enabled                      bool      `yaml:"enabled" json:"enabled"`
	Action                       string    `yaml:"action" json:"action"`
	Script                       string    `yaml:"script,omitempty" json:"script,omitempty"`
	Notifications                []string  `yaml:"notifications,omitempty" json:"notifications,omitempty"`
	ContainerNameFilter          string    `yaml:"containerNameFilter" json:"containerNameFilter"`
	ContainerLabelFilter         string    `yaml:"containerLabelFilter" json:"containerLabelFilter"`
	ContainerEnvVarFilter        string    `yaml:"containerEnvVarFilter" json:"containerEnvVarFilter"`
	StartExited                  bool      `yaml:"startExited" json:"startExited"`
	RetryCount                   int       `yaml:"retryCount,omitempty" json:"retryCount,omitempty"`
	MaxMonitoringDurationMinutes int       `yaml:"maxMonitoringDurationMinutes,omitempty" json:"maxMonitoringDurationMinutes,omitempty"`
	MonitorIntervalSeconds       int       `yaml:"monitorIntervalSeconds,omitempty" json:"monitorIntervalSeconds,omitempty"`
	ActionTimeoutSeconds         int       `yaml:"actionTimeoutSeconds,omitempty" json:"actionTimeoutSeconds,omitempty"`
	ScriptTimeoutSeconds         int       `yaml:"scriptTimeoutSeconds,omitempty" json:"scriptTimeoutSeconds,omitempty"`
	PostActionWaitSeconds        int       `yaml:"postActionWaitSeconds,omitempty" json:"postActionWaitSeconds,omitempty"`
	DockerHostID                 string    `yaml:"dockerHostID,omitempty" json:"dockerHostID,omitempty"`
	CreatedAt                    time.Time `yaml:"createdAt" json:"createdAt"`
	UpdatedAt                    time.Time `yaml:"updatedAt" json:"updatedAt"`
}

const jobsConfigVersion = 1

type JobsFile struct {
	Version int   `yaml:"version"`
	Jobs    []Job `yaml:"jobs"`
}

func LoadJobs(configDir string) ([]Job, error) {
	filePath := filepath.Join(configDir, "jobs.yaml")
	raw, err := os.ReadFile(filePath)

	var fileVersion int
	var jobs []Job

	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read jobs file: %w", err)
		}
		// Migrate from legacy rules.yaml if present.
		legacyPath := filepath.Join(configDir, "rules.yaml")
		raw, err = os.ReadFile(legacyPath)
		if err != nil {
			if os.IsNotExist(err) {
				return []Job{}, nil
			}
			return nil, fmt.Errorf("read jobs file: %w", err)
		}
		var legacy struct {
			Rules []Job `yaml:"rules"`
		}
		if err := yaml.Unmarshal(raw, &legacy); err != nil {
			return nil, fmt.Errorf("parse legacy rules yaml: %w", err)
		}
		if legacy.Rules != nil {
			jobs = legacy.Rules
		}
		fileVersion = 0
	} else {
		var out JobsFile
		if err := yaml.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("parse jobs yaml: %w", err)
		}
		if out.Jobs != nil {
			jobs = out.Jobs
		}
		fileVersion = out.Version
	}

	// Run any pending migrations and write back if the file changed.
	if migrated, updated := migrateJobs(fileVersion, jobs); migrated {
		jobs = updated
		// Best-effort write — if it fails the migration runs again next startup.
		_ = saveJobsVersioned(configDir, jobs)
	}

	return jobs, nil
}

func SaveJobs(configDir string, jobs []Job) error {
	normalized := make([]Job, 0, len(jobs))
	for _, j := range jobs {
		n, err := normalizeJob(j)
		if err != nil {
			return err
		}
		normalized = append(normalized, n)
	}
	return saveJobsVersioned(configDir, normalized)
}

// saveJobsVersioned writes jobs.yaml with the current schema version.
// It skips the normalizeJob pass so migration writes aren't re-validated.
func saveJobsVersioned(configDir string, jobs []Job) error {
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	sorted := make([]Job, len(jobs))
	copy(sorted, jobs)
	sort.SliceStable(sorted, func(i, k int) bool {
		return sorted[i].CreatedAt.Before(sorted[k].CreatedAt)
	})

	payload := JobsFile{Version: jobsConfigVersion, Jobs: sorted}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal jobs yaml: %w", err)
	}

	filePath := filepath.Join(configDir, "jobs.yaml")
	if err := os.WriteFile(filePath, raw, 0o600); err != nil {
		return fmt.Errorf("write jobs yaml: %w", err)
	}
	return nil
}

// migrateJobs applies any schema migrations needed to bring jobs up to
// jobsConfigVersion. Add a new "if fileVersion < N" block for each future
// migration. Returns (true, updated) if any changes were made.
func migrateJobs(fileVersion int, jobs []Job) (migrated bool, result []Job) {
	if fileVersion < 1 {
		// v0→v1: rewrite legacy rule- prefixed IDs to job- prefix.
		for i, j := range jobs {
			if strings.HasPrefix(j.ID, "rule-") {
				jobs[i].ID = "job-" + strings.TrimPrefix(j.ID, "rule-")
				migrated = true
			}
		}
	}
	return migrated, jobs
}

func UpsertJob(existing []Job, in Job) ([]Job, Job, error) {
	normalized, err := normalizeJob(in)
	if err != nil {
		return nil, Job{}, err
	}

	now := time.Now().UTC()
	if normalized.ID == "" {
		normalized.ID = newJobID(now)
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

func DeleteJob(existing []Job, id string) ([]Job, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return existing, false
	}

	out := make([]Job, 0, len(existing))
	removed := false
	for _, j := range existing {
		if j.ID == id {
			removed = true
			continue
		}
		out = append(out, j)
	}
	return out, removed
}

func normalizeJob(in Job) (Job, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Group = strings.TrimSpace(in.Group)
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))
	in.Script = strings.TrimSpace(in.Script)
	deduped := make([]string, 0, len(in.Notifications))
	seen := make(map[string]bool)
	for _, id := range in.Notifications {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			deduped = append(deduped, id)
		}
	}
	in.Notifications = deduped
	in.ContainerNameFilter = strings.TrimSpace(in.ContainerNameFilter)
	in.ContainerLabelFilter = strings.TrimSpace(in.ContainerLabelFilter)
	in.ContainerEnvVarFilter = strings.TrimSpace(in.ContainerEnvVarFilter)

	if in.Name == "" {
		return Job{}, fmt.Errorf("job name is required")
	}
	if in.Action == "" {
		in.Action = "restart"
	}
	if !isSupportedAction(in.Action) {
		return Job{}, fmt.Errorf("unsupported job action %q", in.Action)
	}
	if !jobHasAnyFilter(in) {
		return Job{}, fmt.Errorf("at least one job filter is required")
	}

	return in, nil
}

func isSupportedAction(action string) bool {
	switch action {
	case "restart", "start", "stop", "none", "run-script":
		return true
	default:
		return false
	}
}

func jobHasAnyFilter(j Job) bool {
	return j.ContainerNameFilter != "" || j.ContainerLabelFilter != "" || j.ContainerEnvVarFilter != ""
}

func newJobID(t time.Time) string {
	return fmt.Sprintf("job-%d", t.UnixNano())
}
