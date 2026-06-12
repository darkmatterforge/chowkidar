package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
	"chowkidar/internal/crypto"
	"chowkidar/internal/dockerhealth"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
)

func (a *app) handleHealth(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	exhausted := make([]string, 0)
	for id, s := range a.cState {
		if s.exhausted {
			exhausted = append(exhausted, id)
		}
	}
	resp := map[string]any{
		"ok":                    true,
		"lastScan":              a.lastScan,
		"unhealthyCount":        len(a.unhealthy) + len(a.restarting),
		"exitedCount":           len(a.exited),
		"totalExitedCount":      a.totalExited,
		"action":                cfg.Action,
		"startExited":           cfg.StartExited,
		"retriesExhaustedIDs":   exhausted,
		"monitoringStartsAt":    a.monitorStartsAt,
		"lastScanValid":         !a.lastScan.IsZero(),
		"encryptionKeyMismatch": crypto.HasDecryptionFailures(),
		"version":               appVersion,
		"latestVersion":         a.latestVersion,
		"latestVersionRelDate":  a.latestVersionRelDate,
		"bootTime":              a.bootTime,
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, resp)
}

func (a *app) handleDiagnostics(w http.ResponseWriter, _ *http.Request) {
	cfg := a.getConfig()
	ctx := context.Background()
	d := dockerhealth.Diagnostics{}
	_ = a.retryDocker(ctx, func() error {
		d = dockerhealth.SocketDiagnostics(ctx, a.docker, cfg.DockerSocketPath, cfg.DockerPingTimeoutSeconds)
		if d.DockerReachable {
			return nil
		}
		return errors.New(d.Details)
	})
	logDebugf("api: diagnostics requested mode=%s reachable=%t socketPresent=%t socketWritable=%t details=%q", d.Mode, d.DockerReachable, d.SocketPresent, d.SocketWritable, d.Details)
	writeJSON(w, http.StatusOK, d)
}

func (a *app) enrichContainerServiceMap(ctx context.Context, jobs []config.Job, cfg config.Config, serviceByName map[string]map[string]any) {
	hasEnabledJobs := false
	for _, job := range jobs {
		if job.Enabled {
			hasEnabledJobs = true
			break
		}
	}
	var listed []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		listed, err = a.docker.AllContainers(ctx)
		return err
	}); err != nil {
		return
	}

	type inspectResult struct {
		c        containertypes.Summary
		envKeys  []string
		envPairs []string
	}
	results := make([]inspectResult, len(listed))
	var wg sync.WaitGroup
	for i, c := range listed {
		wg.Add(1)
		go func(i int, c containertypes.Summary) {
			defer wg.Done()
			r := inspectResult{c: c}
			if inspected, err := a.docker.InspectContainer(ctx, c.ID); err == nil && inspected.Config != nil {
				r.envPairs = inspected.Config.Env
				r.envKeys = extractEnvKeys(r.envPairs)
			}
			results[i] = r
		}(i, c)
	}
	wg.Wait()

	for _, r := range results {
		c := r.c
		name := containerName(c)
		matchedJobs := matchedJobsSummary(name, c.Labels, r.envPairs, jobs)
		globalMatch := !hasEnabledJobs && hasAnyFilters(cfg) && matchesConfigFiltersSnapshot(name, c.Labels, r.envPairs, cfg)
		checkEligible := len(matchedJobs) > 0 || globalMatch
		if !checkEligible {
			continue
		}
		checkSource := "global"
		checkAction := cfg.Action
		if len(matchedJobs) > 0 {
			checkSource = "job"
			checkAction = strings.TrimSpace(fmt.Sprint(matchedJobs[0]["action"]))
		}
		serviceByName[name] = map[string]any{
			"id":            c.ID,
			"name":          name,
			"status":        c.Status,
			"state":         c.State,
			"labels":        c.Labels,
			"envKeys":       r.envKeys,
			"matchedJobs":   matchedJobs,
			"checkEligible": checkEligible,
			"checkSource":   checkSource,
			"checkAction":   checkAction,
		}
	}
}

func (a *app) handleContainers(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	containers := make([]map[string]any, 0, len(a.unhealthy))
	serviceByName := make(map[string]map[string]any, len(a.unhealthy)+len(a.exited))
	for _, c := range a.unhealthy {
		name := containerName(c)
		item := map[string]any{
			"id":            c.ID,
			"name":          name,
			"status":        c.Status,
			"state":         c.State,
			"labels":        c.Labels,
			"envKeys":       []string{},
			"matchedJobs":   []map[string]any{},
			"checkEligible": true,
			"checkSource":   "runtime",
			"checkAction":   "",
		}
		containers = append(containers, item)
		serviceByName[name] = item
	}
	exited := make([]map[string]any, 0, len(a.exited))
	for _, c := range a.exited {
		name := containerName(c)
		item := map[string]any{
			"id":            c.ID,
			"name":          name,
			"status":        c.Status,
			"state":         c.State,
			"labels":        c.Labels,
			"envKeys":       []string{},
			"matchedJobs":   []map[string]any{},
			"checkEligible": true,
			"checkSource":   "runtime",
			"checkAction":   "start",
		}
		exited = append(exited, item)
		serviceByName[name] = item
	}
	a.mu.RUnlock()

	cfg := a.getConfig()
	jobs := a.getJobs()
	if a.docker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		a.enrichContainerServiceMap(ctx, jobs, cfg, serviceByName)
	}

	all := make([]map[string]any, 0, len(serviceByName))
	for _, item := range serviceByName {
		all = append(all, item)
	}
	sort.Slice(all, func(i, j int) bool {
		left := strings.ToLower(fmt.Sprint(all[i]["name"]))
		right := strings.ToLower(fmt.Sprint(all[j]["name"]))
		return left < right
	})

	writeJSON(w, http.StatusOK, map[string]any{"unhealthy": containers, "exited": exited, "all": all})
}

func (a *app) handleSettingsPUT(w http.ResponseWriter, r *http.Request) {
	var body config.FileConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	normalizeSettingsBody(&body)

	cfg := a.getConfig()
	logInfof("api: settings update requested action=%s workerCount=%d queueSize=%d", body.Action, body.WorkerCount, body.QueueSize)
	if err := config.SaveFileConfig(cfg.ConfigDir, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// Reload picks up the saved file values plus any env-var overrides.
	// Env vars are authoritative — if WORKER_COUNT is set, it wins even after a UI save.
	reloaded, err := config.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	poolConfigChanged := reloaded.WorkerCount != cfg.WorkerCount || reloaded.QueueSize != cfg.QueueSize

	a.mu.Lock()
	a.cfg = reloaded
	a.httpClient.Timeout = time.Duration(reloaded.HttpClientTimeoutSeconds) * time.Second
	if transport, ok := a.httpClient.Transport.(*http.Transport); ok {
		transport.MaxIdleConns = reloaded.HttpMaxIdleConns
		transport.MaxIdleConnsPerHost = reloaded.HttpMaxIdleConnsPerHost
		transport.IdleConnTimeout = time.Duration(reloaded.HttpIdleConnTimeoutSeconds) * time.Second
	}
	a.notifier = notify.New(buildNotifierServicesFromProfiles(a.notifications))
	a.mu.Unlock()

	applyLogConfig(filepath.Join(reloaded.ConfigDir, "logs"), reloaded)

	// Apply the user-requested log level immediately even if LOG_LEVEL env var
	// overrides it in `reloaded` (env wins on next restart; this change is live-only).
	// Also sync a.cfg.LogLevel so GET /api/settings reflects the active level.
	if body.LogLevel != "" {
		newLevel := parseLogLevel(body.LogLevel)
		if newLevel != configuredLogLevel {
			configuredLogLevel = newLevel
			logInfof("api: log level changed logLevel=%s (env=%q will override on restart if set)",
				logLevelLabel(configuredLogLevel), strings.TrimSpace(os.Getenv("LOG_LEVEL")))
		}
		a.mu.Lock()
		a.cfg.LogLevel = body.LogLevel
		a.mu.Unlock()
	}

	if poolConfigChanged {
		logInfof("api: settings applying worker pool reload oldWorkerCount=%d oldQueueSize=%d newWorkerCount=%d newQueueSize=%d", cfg.WorkerCount, cfg.QueueSize, reloaded.WorkerCount, reloaded.QueueSize)
		a.replacePool(reloaded.WorkerCount, reloaded.QueueSize)
	}
	logInfof("api: settings update applied action=%s workerCount=%d queueSize=%d httpTimeoutSeconds=%d dockerPingTimeoutSeconds=%d dockerSocketPath=%s", reloaded.Action, reloaded.WorkerCount, reloaded.QueueSize, reloaded.HttpClientTimeoutSeconds, reloaded.DockerPingTimeoutSeconds, reloaded.DockerSocketPath)

	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "restartRecommended": true})
}

func (a *app) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Return the effective runtime config so the UI always shows the values
		// that are actually active — including any env-var overrides. On restart
		// env vars win again (envOr), so the UI correctly reflects that too.
		cfg := a.getConfig()
		// Marshal the flat settings struct then inject envOverrides alongside it
		// so existing JS field reads (s.logLevel, s.workerCount, …) are unchanged.
		cfgBytes, _ := json.Marshal(config.ToFileConfig(cfg))
		var cfgMap map[string]any
		_ = json.Unmarshal(cfgBytes, &cfgMap)
		envOverrides := map[string]string{}
		if v := strings.TrimSpace(os.Getenv("LOG_LEVEL")); v != "" {
			envOverrides["logLevel"] = v
		}
		cfgMap["envOverrides"] = envOverrides
		writeJSON(w, http.StatusOK, cfgMap)
	case http.MethodPut:
		a.handleSettingsPUT(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleSettingsTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	if body.Theme != "auto" && body.Theme != "light" && body.Theme != "dark" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "theme must be auto, light, or dark"})
		return
	}
	cfg := a.getConfig()
	fileCfg, err := config.LoadFileConfig(cfg.ConfigDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	fileCfg.Theme = body.Theme
	if err := config.SaveFileConfig(cfg.ConfigDir, fileCfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	a.mu.Lock()
	a.cfg.Theme = body.Theme
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jobs := filterJobsList(a.getJobs(), r.URL.Query())
		total := len(jobs)
		jobs = paginateJobsList(jobs, r.URL.Query())
		writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "total": total})
	case http.MethodPost:
		var in config.Job
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		jobs := a.getJobs()
		next, saved, err := config.UpsertJob(jobs, in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.persistJobs(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleNotifications(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles := a.getNotificationProfiles()
		// Enrich each profile with live usage counters so the UI can show
		// the progress bar without the server needing a separate endpoint.
		type enriched struct {
			config.NotificationProfile
			DailyUsage int `json:"dailyUsage"`
			BurstUsage int `json:"burstUsage"`
		}
		out := make([]enriched, len(profiles))
		today := a.notifDayKey()
		a.notifUsageMu.Lock()
		for i, p := range profiles {
			e := enriched{NotificationProfile: p}
			if u, ok := a.notifUsage[p.ID]; ok {
				if u.dayKey == today {
					e.DailyUsage = u.dayCount
				}
				if p.BurstWindowMinutes > 0 {
					bw := time.Duration(p.BurstWindowMinutes) * time.Minute
					bk := time.Now().UTC().Truncate(bw).Format(time.RFC3339)
					if u.burstKey == bk {
						e.BurstUsage = u.burstCount
					}
				}
			}
			out[i] = e
		}
		a.notifUsageMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
	case http.MethodPut:
		var body struct {
			Profiles []config.NotificationProfile `json:"profiles"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if err := a.persistNotificationProfiles(body.Profiles); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		profiles := a.getNotificationProfiles()
		svc := buildNotifierServicesFromProfiles(profiles)

		a.mu.Lock()
		a.cfg.AppriseServices = svc
		a.notifier = notify.New(svc)
		a.mu.Unlock()

		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "profiles": profiles})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleNotificationByID handles per-profile actions:
//
//	POST /api/notifications/{id}/suspend  body: {"until":"RFC3339 or 'midnight'|'month-end'"}
//	POST /api/notifications/{id}/resume
//	POST /api/notifications/{id}/dismiss-error
func (a *app) handleNotificationByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// parse  /api/notifications/{id}/{action}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/notifications/"), "/", 2)
	if len(parts) != 2 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid path"})
		return
	}
	profileID, action := parts[0], parts[1]

	switch action {
	case "suspend":
		var body struct {
			Until string `json:"until"` // RFC3339 OR "midnight" | "1h" | "24h" | "month-end"
			TZ    string `json:"tz"`    // browser-detected IANA zone (fallback when DisplayTimezone unset)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			logDebugf("notif: suspend — could not decode request body: %v (using defaults)", err)
		}
		var until time.Time
		// Store the browser timezone for future auto-suspends, then resolve.
		a.recordClientTimezone(body.TZ)
		tz := a.resolveTimezone()
		logDebugf("notif: suspend until=%s tz=%s (settings=%s client=%s stored=%s)",
			body.Until, tz, a.getConfig().DisplayTimezone, body.TZ, a.clientTimezone)
		switch body.Until {
		case "", "midnight":
			until = nextMidnight(tz)
		case "month-end":
			until = endOfMonth(tz)
		default:
			// Generic duration patterns: Nm = minutes, Nh = hours, Nd = days, Nw = weeks
			// e.g. "6h", "2d", "3w", "45m"
			d, parseErr := parseSuspendDuration(body.Until)
			if parseErr == nil {
				until = time.Now().Add(d)
			} else {
				// Fall back to RFC3339 literal timestamp
				parsed, err := time.Parse(time.RFC3339, body.Until)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid until value — use Nm/Nh/Nd/Nw, 'midnight', 'month-end', or RFC3339"})
					return
				}
				until = parsed
			}
		}
		a.suspendProfile(profileID, until, "")
		writeJSON(w, http.StatusOK, map[string]any{"suspended": true, "until": until})

	case "resume":
		a.resumeProfile(profileID)
		writeJSON(w, http.StatusOK, map[string]any{"resumed": true})

	case "dismiss-error":
		// Clear the persisted rate-limit error without changing suspension state
		a.mu.Lock()
		profiles := make([]config.NotificationProfile, len(a.notifications))
		copy(profiles, a.notifications)
		for i, p := range profiles {
			if p.ID == profileID {
				profiles[i].LastRateLimitError = ""
				profiles[i].LastRateLimitAt = nil
				break
			}
		}
		a.notifications = profiles
		configDir := a.cfg.ConfigDir
		a.mu.Unlock()
		if err := config.SaveNotificationProfiles(configDir, profiles); err != nil {
			logWarnf("notif: dismiss-error — failed to persist profiles: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"dismissed": true})

	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown action: " + action})
	}
}

func builtInDockerHostProfile(socketPath string) config.DockerHostProfile {
	return config.DockerHostProfile{
		ID:       "local",
		Name:     "Local Docker",
		Type:     "socket",
		Endpoint: strings.TrimSpace(socketPath),
		Enabled:  true,
		BuiltIn:  true,
	}
}

// handleSystemAlerts returns recent critical system events — failed recoveries,
// paused monitoring, and monitoring-started — independently of notification
// agents. These always show in the bell regardless of whether any agent is
// configured or enabled.
func (a *app) handleSystemAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	type SystemAlert struct {
		ID            string    `json:"id"`
		Type          string    `json:"type"`
		ContainerName string    `json:"containerName,omitempty"`
		Message       string    `json:"message"`
		Timestamp     time.Time `json:"timestamp"`
		Action        string    `json:"action,omitempty"`
	}

	var alerts []SystemAlert

	// Monitoring-started event — present once the monitor has run at least once.
	// Use bootTime as the timestamp so it is always earlier than any recovery
	// event (you can't have a recovery failure before monitoring started).
	a.mu.RLock()
	lastScan := a.lastScan
	boot := a.bootTime
	a.mu.RUnlock()
	if !lastScan.IsZero() {
		alerts = append(alerts, SystemAlert{
			ID:        "monitoring-started-" + boot.Format("20060102T150405"),
			Type:      "monitoring_started",
			Message:   "Monitoring is active",
			Timestamp: boot, // boot time, not last-scan — always oldest event
		})
	}

	// Pull recent failed recoveries + paused monitoring from history
	entries, _, err := a.history.ListPage(history.ListOptions{
		Limit:        50,
		OnlyStatuses: []string{"failed", "exhausted"},
	})
	if err == nil {
		seen := make(map[string]bool) // deduplicate by container name
		for _, e := range entries {
			key := e.ContainerName + ":" + e.Status
			if seen[key] {
				continue
			}
			seen[key] = true
			alertType := "failed_recovery"
			msg := "Recovery action failed"
			if e.Status == "exhausted" {
				alertType = "paused_monitoring"
				msg = "Monitoring paused — max retries reached"
			}
			if e.Error != "" {
				msg = e.Error
			}
			alerts = append(alerts, SystemAlert{
				ID:            fmt.Sprintf("%s-%s-%d", e.ContainerID, e.Status, e.Timestamp.Unix()),
				Type:          alertType,
				ContainerName: e.ContainerName,
				Message:       msg,
				Timestamp:     e.Timestamp,
				Action:        e.Action,
			})
		}
	}

	// Append update-available alert when a newer version has been found.
	a.mu.RLock()
	lv := a.latestVersion
	lvd := a.latestVersionRelDate
	a.mu.RUnlock()
	if lv != "" && lv != appVersion {
		alerts = append(alerts, SystemAlert{
			ID:   "update-available-" + lv,
			Type: "update_available",
			Message: fmt.Sprintf("Chowkidar %s is available (current: %s)%s", lv, appVersion, func() string {
				if lvd != "" {
					return " — released " + lvd
				}
				return ""
			}()),
			Timestamp: time.Now(),
		})
	}

	// Upcoming-maintenance reminders — fire at 12h/4h/2h before a window's next
	// scheduled start so users aren't caught off guard by a planned pause.
	// Buckets are ordered tightest-first and only the single closest one that
	// "until" has counted down into is shown — otherwise, once inside the 2h
	// window, all three thresholds are simultaneously satisfied and would all
	// fire at once. Each bucket gets a stable ID keyed on the occurrence's
	// start time, so dismissing a reminder for one occurrence doesn't suppress
	// the next one (and progressing from one bucket to the next surfaces a
	// fresh, undismissed alert since the ID changes).
	type reminderBucket struct {
		id        string
		human     string
		threshold time.Duration
	}
	buckets := []reminderBucket{
		{"2h", "2 hours", 2 * time.Hour},
		{"4h", "4 hours", 4 * time.Hour},
		{"12h", "12 hours", 12 * time.Hour},
	}
	now := time.Now()
	for _, win := range a.getMaintenanceWindows() {
		occurrence, ok := win.NextOccurrence(now)
		if !ok {
			continue
		}
		until := occurrence.Sub(now)
		if until <= 0 {
			continue
		}
		for _, b := range buckets {
			if until <= b.threshold {
				alerts = append(alerts, SystemAlert{
					ID:        fmt.Sprintf("maint-reminder-%s-%d-%s", win.ID, occurrence.Unix(), b.id),
					Type:      "maintenance_upcoming",
					Message:   fmt.Sprintf("%q is scheduled to begin in about %s", win.Title, b.human),
					Timestamp: occurrence,
				})
				break
			}
		}
	}

	// Maintenance lifecycle alerts — completes the started→...→ended story
	// alongside the upcoming reminders above. Each transition is recorded once
	// (in checkMaintenanceTransitions, called once per scan cycle) and kept for
	// ~24h; the stable ID embeds the detected time so it ages out naturally and
	// dismissal survives recomputation.
	a.mu.RLock()
	transitions := append([]maintenanceTransition(nil), a.maintenanceTransitions...)
	a.mu.RUnlock()
	for _, tr := range transitions {
		switch tr.Kind {
		case "started":
			alerts = append(alerts, SystemAlert{
				ID:        fmt.Sprintf("maint-started-%s-%d", tr.WindowID, tr.At.Unix()),
				Type:      "maintenance_started",
				Message:   fmt.Sprintf("%q has started — affected services are now paused", tr.Title),
				Timestamp: tr.At,
			})
		case "ended":
			alerts = append(alerts, SystemAlert{
				ID:        fmt.Sprintf("maint-ended-%s-%d", tr.WindowID, tr.At.Unix()),
				Type:      "maintenance_ended",
				Message:   fmt.Sprintf("%q has ended — affected services have resumed", tr.Title),
				Timestamp: tr.At,
			})
		}
	}

	// Filter out dismissed alerts before returning.
	a.dismissedMu.Lock()
	dismissed := a.dismissedAlerts
	a.dismissedMu.Unlock()
	visible := alerts[:0]
	for _, al := range alerts {
		if _, ok := dismissed[al.ID]; !ok {
			visible = append(visible, al)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": visible})
}

// handleSystemAlertsDismiss handles POST /api/system-alerts/dismiss.
// Body: {"ids":["id1","id2"]} — marks alerts as dismissed server-side.
func (a *app) handleSystemAlertsDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ids array required"})
		return
	}
	now := time.Now().UTC()
	a.dismissedMu.Lock()
	if a.dismissedAlerts == nil {
		a.dismissedAlerts = make(map[string]time.Time)
	}
	for _, id := range body.IDs {
		a.dismissedAlerts[id] = now
	}
	snapshot := make(map[string]time.Time, len(a.dismissedAlerts))
	maps.Copy(snapshot, a.dismissedAlerts)
	a.dismissedMu.Unlock()
	if err := saveDismissedAlerts(a.cfg.ConfigDir, snapshot); err != nil {
		logWarnf("dismissed alerts: save failed: %v", err)
	}
	logInfof("api: dismissed alerts count=%d", len(body.IDs))
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": len(body.IDs)})
}

func (a *app) handleDockerHostsGET(w http.ResponseWriter) {
	cfg := a.getConfig()
	profiles := a.getDockerHostProfiles()
	builtIn := builtInDockerHostProfile(cfg.DockerSocketPath)
	all := append([]config.DockerHostProfile{builtIn}, profiles...)
	writeJSON(w, http.StatusOK, map[string]any{"profiles": all})
}

func (a *app) handleDockerHostsPUT(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profiles []config.DockerHostProfile `json:"profiles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}

	// Determine which user-defined host IDs are being removed.
	newIDs := make(map[string]bool, len(body.Profiles))
	for _, p := range body.Profiles {
		newIDs[p.ID] = true
	}
	current := a.getDockerHostProfiles()
	var removed []string
	for _, p := range current {
		if !p.BuiltIn && p.ID != "local" && !newIDs[p.ID] {
			removed = append(removed, p.ID)
		}
	}

	// Block deletion if any job still targets the host being removed.
	if len(removed) > 0 {
		removedSet := make(map[string]bool, len(removed))
		for _, id := range removed {
			removedSet[id] = true
		}
		var blocking []string
		for _, job := range a.getJobs() {
			for _, hid := range job.DockerHostIDs {
				if removedSet[hid] {
					blocking = append(blocking, job.Name)
					break
				}
			}
		}
		if len(blocking) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "Cannot delete Docker host: it is still referenced by jobs. " +
					"Update or remove those jobs first.",
				"jobs": blocking,
			})
			return
		}
	}

	toSave := make([]config.DockerHostProfile, 0)
	for _, p := range body.Profiles {
		if !p.BuiltIn && p.ID != "local" {
			toSave = append(toSave, p)
		}
	}
	if err := a.persistDockerHostProfiles(toSave); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	cfg := a.getConfig()
	savedProfiles := append([]config.DockerHostProfile{builtInDockerHostProfile(cfg.DockerSocketPath)}, a.getDockerHostProfiles()...)
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "profiles": savedProfiles})
}

func (a *app) handleDockerHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleDockerHostsGET(w)
	case http.MethodPut:
		a.handleDockerHostsPUT(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleDockerHostsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := a.getConfig()
	profiles := a.getDockerHostProfiles()

	allHosts := make([]config.DockerHostProfile, 0, len(profiles)+1)
	allHosts = append(allHosts, builtInDockerHostProfile(cfg.DockerSocketPath))
	allHosts = append(allHosts, profiles...)

	type hostStatus struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Enabled   bool   `json:"enabled"`
		Connected bool   `json:"connected"`
		Info      string `json:"info"`
		ErrMsg    string `json:"error,omitempty"`
	}

	results := make([]hostStatus, len(allHosts))
	var wg sync.WaitGroup
	for i, h := range allHosts {
		wg.Add(1)
		go func(idx int, host config.DockerHostProfile) {
			defer wg.Done()
			s := hostStatus{ID: host.ID, Name: host.Name, Enabled: host.Enabled}
			if !host.Enabled {
				results[idx] = s
				return
			}
			ok, info, err := dockerhealth.PingHost(r.Context(), host.Type, host.Endpoint, cfg.DockerPingTimeoutSeconds, nil)
			s.Connected = ok
			s.Info = info
			if err != nil {
				s.ErrMsg = err.Error()
			}
			results[idx] = s
		}(i, h)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{"hosts": results})
}

func (a *app) handleTestDockerHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Type          string `json:"type"`
		Endpoint      string `json:"endpoint"`
		TLSCACert     string `json:"tlsCACert"`
		TLSCert       string `json:"tlsCert"`
		TLSKey        string `json:"tlsKey"`
		TLSSkipVerify bool   `json:"tlsSkipVerify"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	hostType := strings.ToLower(strings.TrimSpace(body.Type))
	endpoint := strings.TrimSpace(body.Endpoint)
	if endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "endpoint is required"})
		return
	}
	var tlsCfg *dockerhealth.TLSConfig
	if body.TLSCACert != "" || body.TLSCert != "" || body.TLSKey != "" || body.TLSSkipVerify {
		tlsCfg = &dockerhealth.TLSConfig{
			CACert:     body.TLSCACert,
			Cert:       body.TLSCert,
			Key:        body.TLSKey,
			SkipVerify: body.TLSSkipVerify,
		}
	}
	cfg := a.getConfig()
	ok, info, err := dockerhealth.PingHost(r.Context(), hostType, endpoint, cfg.DockerPingTimeoutSeconds, tlsCfg)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "info": info})
}

func (a *app) handleScripts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"scripts": a.getScriptEntries()})
	case http.MethodPut:
		var body struct {
			Scripts []config.ScriptEntry `json:"scripts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if err := a.persistScriptEntries(body.Scripts); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "scripts": a.getScriptEntries()})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// ensureDryRunContainer returns a real running container called chowkidar-dry-run.
// It creates one via the docker CLI if it doesn't exist yet, so that scripts
// running in dry-run mode can use docker inspect, docker ps, etc. against a
// real container. Falls back to a dummy summary when Docker is unavailable.
func (a *app) ensureDryRunContainer(ctx context.Context) containertypes.Summary {
	const name = "chowkidar-dry-run"
	dummy := containertypes.Summary{ID: "dry-run-test", Names: []string{"/" + name}}

	if a.docker != nil {
		all, err := a.docker.AllContainers(ctx)
		if err == nil {
			for _, c := range all {
				for _, n := range c.Names {
					if strings.TrimPrefix(n, "/") == name {
						return c
					}
				}
			}
		}
	}

	// Container not found — create it via docker CLI (available in the image).
	out, err := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", name,
		"--label", "chowkidar.managed=true",
		"--health-cmd", "echo ok",
		"--health-interval", "30s",
		"alpine:3", "sh", "-c", "while true; do sleep 5; done",
	).CombinedOutput()
	if err != nil {
		// Container may already exist but be stopped — try starting it.
		if strings.Contains(string(out), "already in use") {
			_ = exec.CommandContext(ctx, "docker", "start", name).Run()
			// Re-query
			if a.docker != nil {
				if all, err2 := a.docker.AllContainers(ctx); err2 == nil {
					for _, c := range all {
						for _, n := range c.Names {
							if strings.TrimPrefix(n, "/") == name {
								return c
							}
						}
					}
				}
			}
		}
		logWarnf("dry-run: could not create %s container: %v — using dummy", name, err)
		return dummy
	}

	id := strings.TrimSpace(string(out))
	return containertypes.Summary{ID: id, Names: []string{"/" + name}, State: "running"}
}

// scheduleDryRunCleanup resets (or creates) a timer that removes the
// chowkidar-dry-run container after the given idle duration.
func (a *app) scheduleDryRunCleanup(after time.Duration) {
	a.dryRunCleanupMu.Lock()
	defer a.dryRunCleanupMu.Unlock()
	if a.dryRunCleanupTimer != nil {
		a.dryRunCleanupTimer.Reset(after)
		return
	}
	a.dryRunCleanupTimer = time.AfterFunc(after, func() {
		a.dryRunCleanupMu.Lock()
		a.dryRunCleanupTimer = nil
		a.dryRunCleanupMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "docker", "rm", "-f", "chowkidar-dry-run").CombinedOutput()
		if err != nil {
			logWarnf("dry-run cleanup: %v — %s", err, strings.TrimSpace(string(out)))
		} else {
			logInfof("dry-run cleanup: removed chowkidar-dry-run container")
		}
	})
}

func (a *app) handleScriptDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Script string `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Script) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "script is required"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	// Use a real dry-run container so docker commands (inspect, ps, etc.) work.
	// DRY_RUN=1 is still injected so templates with a guard exit early safely.
	target := a.ensureDryRunContainer(ctx)
	output, err := a.executeRunScript(ctx, body.Script, a.getConfig(), target, containerName(target), true)

	// Schedule removal of the dry-run container 90 s after the last run.
	a.scheduleDryRunCleanup(90 * time.Second)

	resp := map[string]any{"output": output}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleScriptDryRunCleanup handles DELETE /api/scripts/dry-run.
// It immediately removes the chowkidar-dry-run container rather than waiting
// for the automatic 90-second idle timer.  Used by tests to clean up promptly.
func (a *app) handleScriptDryRunCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Cancel any pending timer so it doesn't interfere.
	a.cancelDryRunCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", "chowkidar-dry-run").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such container") {
			writeJSON(w, http.StatusOK, map[string]any{"removed": false, "reason": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": msg})
		return
	}
	logInfof("dry-run cleanup: removed chowkidar-dry-run container via API")
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (a *app) handleJobByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "job id is required"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		var in config.Job
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		in.ID = id

		jobs := a.getJobs()
		next, saved, err := config.UpsertJob(jobs, in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.persistJobs(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		jobs := a.getJobs()
		next, removed := config.DeleteJob(jobs, id)
		if !removed {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
			return
		}
		if err := a.persistJobs(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleHistoryEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleHistory(w, r)
	case http.MethodPost:
		// Accepts a JSON array of history entries for bulk insertion.
		// Used by the Playwright global setup to seed test data before the suite runs.
		var entries []history.Entry
		if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
			return
		}
		for i := range entries {
			if err := a.history.Append(entries[i]); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}
		logInfof("api: history seeded count=%d", len(entries))
		writeJSON(w, http.StatusCreated, map[string]any{"seeded": len(entries)})
	case http.MethodDelete:
		if err := a.history.Clear(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		logInfof("api: history cleared")
		writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	isBars := q.Get("bars") == "true"

	// Bars requests (sparklines) allow a higher limit so per-service history
	// is not crowded out by other services' events when many containers exist.
	maxLimit := 500
	if isBars {
		maxLimit = 2000
	}
	limit := 25
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= maxLimit {
			limit = parsed
		}
	}

	page := 1
	if raw := strings.TrimSpace(q.Get("page")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}

	offset := (page - 1) * limit

	service := strings.TrimSpace(q.Get("service"))

	// ?bars=true is used by the dashboard sparklines — include healthy pulse
	// events so bars turn green for containers with no action history.
	// The activity feed always excludes healthy to keep it readable.
	excludeStatus := "healthy"
	if isBars {
		excludeStatus = ""
	}
	entries, total, err := a.history.ListPage(history.ListOptions{
		Limit:         limit,
		Offset:        offset,
		Service:       service,
		ExcludeStatus: excludeStatus,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

func (a *app) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProfileID string `json:"profileId"`
		Service   string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}

	const testMsg = "This is a test notification from Chowkidar"

	cfg := a.getConfig()
	prefix := "[chowkidar]"
	if strings.TrimSpace(cfg.ExternalHostname) != "" {
		prefix = "[chowkidar@" + strings.TrimSpace(cfg.ExternalHostname) + "]"
	}

	if strings.TrimSpace(req.Service) != "" {
		svc := strings.TrimSpace(req.Service)
		logInfof("notify: test direct service=%s", svc)
		n := notify.New(svc)
		if err := n.Send(prefix+" test", testMsg); err != nil {
			logWarnf("notify: test failed service=%s err=%v", svc, err)
			writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": err.Error()})
			return
		}
		logInfof("notify: test ok service=%s", svc)
		writeJSON(w, http.StatusOK, map[string]any{"sent": true})
		return
	}

	if strings.TrimSpace(req.ProfileID) != "" {
		profiles := a.getNotificationProfiles()
		var found *config.NotificationProfile
		for i := range profiles {
			if profiles[i].ID == req.ProfileID {
				found = &profiles[i]
				break
			}
		}
		if found == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"sent": false, "error": "profile not found"})
			return
		}
		if strings.TrimSpace(found.Service) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": "profile has no service URL configured"})
			return
		}
		logInfof("notify: test profile=%s service=%s", found.ID, found.Service)
		n := notify.New(found.Service)
		if err := n.Send(prefix+" test", testMsg); err != nil {
			logWarnf("notify: test failed profile=%s err=%v", found.ID, err)
			writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": err.Error()})
			return
		}
		logInfof("notify: test ok profile=%s", found.ID)
		writeJSON(w, http.StatusOK, map[string]any{"sent": true})
		return
	}

	logInfof("notify: global test")
	if err := a.sendNotification("test", testMsg); err != nil {
		logWarnf("notify: global test failed err=%v", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": err.Error()})
		return
	}
	logInfof("notify: global test ok")
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func (a *app) handleManualAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Container string `json:"container"`
		Name      string `json:"name"`
		Action    string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	req.Container = strings.TrimSpace(req.Container)
	if req.Container == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "container is required"})
		return
	}

	resolvedName := strings.TrimSpace(req.Name)
	if resolvedName == "" {
		resolvedName = a.containerNameByID(req.Container)
	}
	c := containertypes.Summary{ID: req.Container, Names: []string{"/" + resolvedName}}
	reason := "manual " + req.Action
	logInfof("api: manual action queued container=%s action=%s", resolvedName, req.Action)
	a.enqueueJob(actionJob{app: a, container: c, reason: reason, action: req.Action, force: true})
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (a *app) handleResetCooldown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/containers/")
	id = strings.TrimSuffix(id, "/reset-cooldown")
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "container id is required"})
		return
	}
	name := a.containerNameByID(id)
	a.resetActionCooldown(name)
	logInfof("api: resume monitoring requested container=%s id=%.12s", name, id)
	a.logActionHistory(
		containertypes.Summary{ID: id, Names: []string{"/" + name}},
		"resume monitoring", "resume", 0, "success", "", "", 0,
	)
	writeJSON(w, http.StatusOK, map[string]any{"reset": true})
}
