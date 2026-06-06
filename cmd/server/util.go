package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
)

func extractEnvKeys(envPairs []string) []string {
	if len(envPairs) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(envPairs))
	seen := make(map[string]struct{}, len(envPairs))
	for _, pair := range envPairs {
		key, _, _ := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func matchedJobsSummary(name string, labels map[string]string, envPairs []string, jobs []config.Job) []map[string]any {
	matches := make([]map[string]any, 0)
	for _, r := range jobs {
		if !r.Enabled {
			continue
		}
		if !jobMatchesServiceSnapshot(name, labels, envPairs, r) {
			continue
		}
		matches = append(matches, map[string]any{
			"id":                     r.ID,
			"name":                   r.Name,
			"group":                  r.Group,
			"action":                 r.Action,
			"dockerHostIDs":          r.DockerHostIDs,
			"monitorIntervalSeconds": r.MonitorIntervalSeconds,
		})
	}
	return matches
}

func jobMatchesServiceSnapshot(name string, labels map[string]string, envPairs []string, r config.Job) bool {
	if r.ContainerNameFilter != "" {
		allowed := splitCSV(r.ContainerNameFilter)
		ok := false
		for _, x := range allowed {
			if strings.Contains(name, x) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if r.ContainerLabelFilter != "" {
		ok := false
		for k, v := range labels {
			if strings.Contains(k+"="+v, r.ContainerLabelFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if r.ContainerEnvVarFilter != "" {
		ok := false
		for _, envPair := range envPairs {
			if strings.Contains(envPair, r.ContainerEnvVarFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	return true
}

func matchesConfigFiltersSnapshot(name string, labels map[string]string, envPairs []string, cfg config.Config) bool {
	if cfg.ContainerNameFilter != "" {
		allowed := splitCSV(cfg.ContainerNameFilter)
		ok := false
		for _, x := range allowed {
			if strings.Contains(name, x) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if cfg.ContainerLabelFilter != "" {
		ok := false
		for k, v := range labels {
			if strings.Contains(k+"="+v, cfg.ContainerLabelFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if cfg.ContainerEnvVarFilter != "" {
		ok := false
		for _, envPair := range envPairs {
			if strings.Contains(envPair, cfg.ContainerEnvVarFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	return true
}

func normalizeSettingsBody(body *config.FileConfig) {
	body.Action = strings.ToLower(strings.TrimSpace(body.Action))
	if body.Action == "" {
		body.Action = "restart"
	}
	if body.WorkerCount < 1 {
		body.WorkerCount = 2
	}
	if body.QueueSize < 1 {
		body.QueueSize = 64
	}
	if body.HttpClientTimeoutSeconds < 1 {
		body.HttpClientTimeoutSeconds = 15
	}
	if body.HttpMaxIdleConns < 1 {
		body.HttpMaxIdleConns = 20
	}
	if body.HttpMaxIdleConnsPerHost < 1 {
		body.HttpMaxIdleConnsPerHost = 10
	}
	if body.HttpIdleConnTimeoutSeconds < 1 {
		body.HttpIdleConnTimeoutSeconds = 90
	}
	if body.DockerPingTimeoutSeconds < 1 {
		body.DockerPingTimeoutSeconds = 5
	}
	if body.DockerClientRetryCount < 1 {
		body.DockerClientRetryCount = 1
	}
	if body.DockerClientRetryDelaySeconds < 1 {
		body.DockerClientRetryDelaySeconds = 2
	}
	if body.ActionTimeoutSeconds < 1 {
		body.ActionTimeoutSeconds = 20
	}
	if body.LogRetentionDays < 1 {
		body.LogRetentionDays = 7
	}
	body.DockerSocketPath = strings.TrimSpace(body.DockerSocketPath)
	if body.DockerSocketPath == "" {
		body.DockerSocketPath = "/var/run/docker.sock"
	}
	switch body.Theme {
	case "light", "dark", "auto":
	default:
		body.Theme = "auto"
	}
	switch body.DashboardLayout {
	case "cards", "table", "grid":
	default:
		body.DashboardLayout = "cards"
	}
}


func jobMatchesFilters(job config.Job, qStr, actionFilter, enabledFilter, groupFilter, hostIDFilter string) bool {
	if actionFilter != "" && strings.ToLower(job.Action) != actionFilter {
		return false
	}
	if enabledFilter == "true" && !job.Enabled {
		return false
	}
	if enabledFilter == "false" && job.Enabled {
		return false
	}
	if groupFilter != "" && strings.ToLower(strings.TrimSpace(job.Group)) != groupFilter {
		return false
	}
	if hostIDFilter != "" && len(job.DockerHostIDs) > 0 {
		found := false
		for _, id := range job.DockerHostIDs {
			if id == hostIDFilter {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if qStr != "" {
		haystack := strings.ToLower(strings.Join([]string{
			job.Name, job.Group, job.Action,
			job.ContainerNameFilter, job.ContainerLabelFilter, job.ContainerEnvVarFilter,
		}, " "))
		return strings.Contains(haystack, qStr)
	}
	return true
}

func filterJobsList(jobs []config.Job, q url.Values) []config.Job {
	qStr := strings.ToLower(strings.TrimSpace(q.Get("q")))
	actionFilter := strings.ToLower(strings.TrimSpace(q.Get("action")))
	enabledFilter := strings.ToLower(strings.TrimSpace(q.Get("enabled")))
	groupFilter := strings.ToLower(strings.TrimSpace(q.Get("group")))
	hostIDFilter := strings.TrimSpace(q.Get("hostID"))
	if qStr == "" && actionFilter == "" && enabledFilter == "" && groupFilter == "" && hostIDFilter == "" {
		return jobs
	}
	filtered := make([]config.Job, 0, len(jobs))
	for _, job := range jobs {
		if jobMatchesFilters(job, qStr, actionFilter, enabledFilter, groupFilter, hostIDFilter) {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

func paginateJobsList(jobs []config.Job, q url.Values) []config.Job {
	limit := 0
	offset := 0
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("limit"))); err == nil && n > 0 {
		limit = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("offset"))); err == nil && n >= 0 {
		offset = n
	}
	if offset == 0 && limit == 0 {
		return jobs
	}
	if offset >= len(jobs) {
		return []config.Job{}
	}
	jobs = jobs[offset:]
	if limit > 0 && limit < len(jobs) {
		jobs = jobs[:limit]
	}
	return jobs
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logDebugf("writeJSON: encode error: %v", err)
	}
}

// isSecureRequest returns true when the connection is HTTPS, either directly
// or via a reverse proxy that sets X-Forwarded-Proto.
func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func containerName(c containertypes.Summary) string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// parseExitCode extracts the exit code from a Docker status string like "Exited (1) 2 minutes ago".
// Returns -1 if the string is not in that format.
func parseExitCode(status string) int {
	idx1 := strings.Index(status, "(")
	idx2 := strings.Index(status, ")")
	if idx1 < 0 || idx2 <= idx1 {
		return -1
	}
	code, err := strconv.Atoi(strings.TrimSpace(status[idx1+1 : idx2]))
	if err != nil {
		return -1
	}
	return code
}

// effectiveRetryCount returns the retry limit for a job, computing it from
// MaxMonitoringDurationMinutes when set (maxRetries = duration / scanInterval).
func effectiveRetryCount(r *config.Job) int {
	if r == nil {
		return 0
	}
	if r.MaxMonitoringDurationMinutes > 0 {
		interval := time.Duration(r.MonitorIntervalSeconds) * time.Second
		if interval < 5*time.Second {
			interval = 60 * time.Second
		}
		n := max(int(time.Duration(r.MaxMonitoringDurationMinutes)*time.Minute/interval), 1)
		return n
	}
	if r.RetryCount > 0 {
		return r.RetryCount
	}
	return 0
}

func splitCSV(in string) []string {
	parts := strings.Split(in, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hasAnyFilters(cfg config.Config) bool {
	return strings.TrimSpace(cfg.ContainerNameFilter) != "" || strings.TrimSpace(cfg.ContainerLabelFilter) != "" || strings.TrimSpace(cfg.ContainerEnvVarFilter) != ""
}

func hasAnyJobFilters(jobs []config.Job) bool {
	for _, r := range jobs {
		if strings.TrimSpace(r.ContainerNameFilter) != "" || strings.TrimSpace(r.ContainerLabelFilter) != "" || strings.TrimSpace(r.ContainerEnvVarFilter) != "" {
			return true
		}
	}
	return false
}
