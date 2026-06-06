package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
	"chowkidar/internal/dockerhealth"
)

type recoveredContainer struct {
	name          string
	notifications []string
	ruleName      string
}

// healthCheckMode identifies how a job determines container health.
// Add a new constant here (and a matching case in jobHealthCheckMode + scanHost)
// to introduce additional health-check strategies in the future.
type healthCheckMode string

const (
	// healthCheckNative uses Docker's built-in health status (default).
	healthCheckNative healthCheckMode = "native"
	// healthCheckScript runs a user-defined bash script; exit 0 = healthy, non-zero = unhealthy.
	healthCheckScript healthCheckMode = "script"
)

// jobSelection is the result of matching a container against configured jobs and global filters.
// ok is false when no match was found. job is nil for global-config fallback matches.
type jobSelection struct {
	action        string
	script        string
	notifications []string
	jobID         string
	jobName       string
	job           *config.Job // nil for global-config fallback
	ok            bool
}

func shouldSendStartupNotification(d dockerhealth.Diagnostics) bool {
	return d.DockerReachable
}

func (a *app) sendStartupNotificationIfReady() error {
	cfg := a.getConfig()
	if !cfg.BootNotification {
		return nil
	}
	if !shouldSendStartupNotification(a.diagnostics) {
		logWarnf("startup notification skipped: diagnostics not ready (%s)", strings.TrimSpace(a.diagnostics.Details))
		return nil
	}
	if len(cfg.BootNotificationProfiles) > 0 {
		return a.sendJobNotifications("startup", notifData{}, cfg.BootNotificationProfiles)
	}
	return a.sendNotification("startup", "Application started and monitor loop initialized")
}

func (a *app) monitorLoop(stop <-chan struct{}) {
	if d := a.getConfig().StartupDelaySeconds; d > 0 {
		logInfof("monitor: startup delay seconds=%d", d)
		startsAt := time.Now().Add(time.Duration(d) * time.Second)
		a.mu.Lock()
		a.monitorStartsAt = startsAt
		a.mu.Unlock()
		select {
		case <-stop:
			a.mu.Lock()
			a.monitorStartsAt = time.Time{}
			a.mu.Unlock()
			logInfof("monitor: stop received during startup delay")
			return
		case <-time.After(time.Duration(d) * time.Second):
			a.mu.Lock()
			a.monitorStartsAt = time.Time{}
			a.mu.Unlock()
			logInfof("monitor: startup delay elapsed, beginning monitor")
		}
	}
	if err := a.sendStartupNotificationIfReady(); err != nil {
		logWarnf("monitor: startup notification failed: %v", err)
	}
	for {
		logInfof("monitor: scan cycle start")
		a.scanOnce()
		sleep := a.timeUntilNextJobDue()
		logInfof("monitor: next scan in %s", sleep.Round(time.Second))
		select {
		case <-stop:
			logInfof("monitor: stop signal received, exiting loop")
			return
		case <-time.After(sleep):
		}
	}
}

func (a *app) timeUntilNextJobDue() time.Duration {
	jobs := a.getJobs()
	a.mu.RLock()
	defer a.mu.RUnlock()

	now := time.Now()
	var next time.Time
	for _, r := range jobs {
		if !r.Enabled {
			continue
		}
		interval := time.Duration(r.MonitorIntervalSeconds) * time.Second
		if interval < 5*time.Second {
			interval = 60 * time.Second
		}
		due := a.lastJobScan[r.ID].Add(interval)
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	if next.IsZero() {
		return 60 * time.Second // no jobs: check back in 60s in case jobs are added
	}
	if d := next.Sub(now); d > 0 {
		return d
	}
	return time.Second // already overdue, fire immediately after 1s floor
}

func (a *app) retryDocker(ctx context.Context, fn func() error) error {
	cfg := a.getConfig()
	retries := max(cfg.DockerClientRetryCount, 1)
	delay := max(cfg.DockerClientRetryDelaySeconds, 2)
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			logWarnf("docker: retry aborted contextErr=%v", err)
			return err
		}
		if attempt > 1 {
			logDebugf("docker: retry attempt %d/%d", attempt, retries)
			time.Sleep(time.Duration(delay) * time.Second)
		}
		if err := fn(); err != nil {
			lastErr = err
			logWarnf("docker: operation failed attempt %d/%d err=%v", attempt, retries, err)
			continue
		}
		if attempt > 1 {
			logInfof("docker: operation succeeded after retries attempts=%d", attempt)
		}
		return nil
	}
	return lastErr
}

// computeDueJobs returns the subset of jobs whose scan interval has elapsed and
// updates a.lastJobScan under the write lock.
func (a *app) computeDueJobs(jobs []config.Job) ([]config.Job, map[string]bool) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	dueJobs := make([]config.Job, 0, len(jobs))
	dueJobIDs := make(map[string]bool, len(jobs))
	for _, r := range jobs {
		if !r.Enabled {
			continue
		}
		interval := time.Duration(r.MonitorIntervalSeconds) * time.Second
		if interval < 5*time.Second {
			interval = 60 * time.Second
		}
		last, seen := a.lastJobScan[r.ID]
		if !seen || now.Sub(last) >= interval {
			dueJobs = append(dueJobs, r)
			dueJobIDs[r.ID] = true
			a.lastJobScan[r.ID] = now
		}
	}
	return dueJobs, dueJobIDs
}

// enqueueIfReady checks retries-exhausted and post-action-wait before enqueuing.
// The caller is responsible for checking whether the job is due.
// Returns true if the action was queued, false if blocked by exhaustion or cooldown.
func (a *app) enqueueIfReady(c containertypes.Summary, reason string, sel jobSelection) bool {
	name := containerName(c)
	if a.isRetriesExhausted(name) {
		logInfof("monitor: skip container=%s reason=retries-exhausted", name)
		return false
	}
	if remaining := a.postActionWaitRemaining(name); remaining > 0 {
		logInfof("monitor: skip container=%s reason=post-action-wait remaining=%s", name, remaining.Round(time.Second))
		a.logActionHistory(c, reason, sel.action, 0, "skipped", "cooldown active", "", 0)
		return false
	}
	job := actionJob{
		app:           a,
		container:     c,
		reason:        reason,
		action:        sel.action,
		script:        sel.script,
		notifications: sel.notifications,
		jobID:         sel.jobID,
		jobName:       sel.jobName,
	}
	applyMatchedJobSettings(&job, sel.job)
	if sel.job != nil && sel.job.StableScansRequired > 0 {
		a.setStableScansRequired(name, sel.job.StableScansRequired)
	}
	logInfof("monitor: queueing container=%s reason=%s action=%s jobID=%s jobName=%s retryCount=%d postWait=%ds",
		name, reason, sel.action, sel.jobID, sel.jobName, job.retryCount, job.postActionWaitSeconds)
	a.enqueueJob(job)
	return true
}

func (a *app) processUnhealthyContainers(ctx context.Context, docker dockerClient, hostID string, containers []containertypes.Summary, cfg config.Config, jobs []config.Job, dueJobIDs map[string]bool) []containertypes.Summary {
	filtered := make([]containertypes.Summary, 0, len(containers))
	for _, c := range containers {
		name := containerName(c)
		logInfof("monitor: evaluating unhealthy container=%s id=%.12s state=%s status=%s host=%s", name, c.ID, c.State, c.Status, hostID)
		if a.isSelfContainer(c) {
			logDebugf("monitor: skip self container=%s id=%.12s", name, c.ID)
			continue
		}
		sel := a.selectActionForContainer(ctx, docker, c, cfg, jobs)
		if !sel.ok {
			logDebugf("monitor: no job/filter match container=%s — skipping", name)
			continue
		}
		filtered = append(filtered, c)
		if !dueJobIDs[sel.jobID] {
			logDebugf("monitor: job not due container=%s jobID=%s", name, sel.jobID)
			continue
		}
		if a.enqueueIfReady(c, "unhealthy", sel) && a.shouldNotify(name) {
			if err := a.sendJobNotifications("unhealthy detected", notifData{
				ContainerName: name,
				RuleName:      sel.jobName,
				Reason:        "unhealthy",
				MaxRetries:    effectiveRetryCount(sel.job),
			}, sel.notifications); err != nil {
				logWarnf("notify: job send failed event=unhealthy container=%s err=%v", name, err)
			}
		}
	}
	return filtered
}

// processRestartingContainers queries stuck-restarting containers, enqueues actions for
// chowkidar-managed ones, and returns all restarting containers plus the matched subset.
func (a *app) processRestartingContainers(ctx context.Context, docker dockerClient, hostID string, cfg config.Config, jobs []config.Job, dueJobIDs map[string]bool) (restarting, matched []containertypes.Summary) {
	if err := a.retryDocker(ctx, func() error {
		var err error
		restarting, err = docker.RestartingContainers(ctx)
		return err
	}); err != nil {
		logErrorf("failed to query restarting containers host=%s: %v", hostID, err)
	}
	for _, c := range restarting {
		name := containerName(c)
		if a.isSelfContainer(c) {
			continue
		}
		a.mu.RLock()
		s := a.cState[name]
		inCycle := s != nil && s.cycle > 0
		a.mu.RUnlock()
		if !inCycle {
			continue
		}
		logInfof("monitor: evaluating stuck-restarting container=%s id=%.12s host=%s", name, c.ID, hostID)
		sel := a.selectActionForContainer(ctx, docker, c, cfg, jobs)
		if !sel.ok {
			continue
		}
		matched = append(matched, c)
		if !dueJobIDs[sel.jobID] {
			continue
		}
		a.enqueueIfReady(c, "stuck-restarting", sel)
	}
	return restarting, matched
}

func (a *app) processCrashedContainer(ctx context.Context, docker dockerClient, hostID string, c containertypes.Summary, cfg config.Config, jobs []config.Job, dueJobIDs map[string]bool, exitCode int) bool {
	name := containerName(c)
	reason := fmt.Sprintf("crash (exit %d)", exitCode)
	sel := a.selectActionForContainer(ctx, docker, c, cfg, jobs)
	if !sel.ok {
		logDebugf("monitor: crashed container=%s exitCode=%d host=%s — no job match, skipping", name, exitCode, hostID)
		return false
	}
	if !dueJobIDs[sel.jobID] {
		logDebugf("monitor: job not due container=%s jobID=%s", name, sel.jobID)
		return true // matched but not due — still add to exitedFiltered
	}
	a.enqueueIfReady(c, reason, sel)
	return true
}

func (a *app) processExitedContainers(ctx context.Context, docker dockerClient, hostID string, cfg config.Config, jobs []config.Job, dueJobs []config.Job, dueJobIDs map[string]bool) []containertypes.Summary {
	exitedFiltered := make([]containertypes.Summary, 0)
	if !cfg.StartExited && len(jobs) == 0 {
		return exitedFiltered
	}
	if cfg.RequireFilterForExited && !hasAnyFilters(cfg) && !hasAnyJobFilters(jobs) {
		logWarnf("start-exited enabled but no filters configured; skipping exited container scan for safety")
		return exitedFiltered
	}
	var exited []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		exited, err = docker.ExitedContainers(ctx)
		return err
	}); err != nil {
		logErrorf("failed to query exited containers: %v", err)
		return exitedFiltered
	}
	logDebugf("monitor: scan fetched exited=%d host=%s", len(exited), hostID)
	// Deduplicate by name: if multiple stopped containers share a name (old instances),
	// keep only the newest one to avoid duplicate processing and history entries.
	seen := make(map[string]bool, len(exited))
	cleanExitCount := 0
	for _, c := range exited {
		name := containerName(c)
		if a.isSelfContainer(c) {
			logDebugf("monitor: skip self exited container=%s id=%.12s", name, c.ID)
			continue
		}
		if seen[name] {
			logDebugf("monitor: skip duplicate exited container=%s id=%.12s", name, c.ID)
			continue
		}
		seen[name] = true
		exitCode := parseExitCode(c.Status)
		logInfof("monitor: evaluating exited container=%s id=%.12s state=%s status=%s exitCode=%d", name, c.ID, c.State, c.Status, exitCode)
		if exitCode == 0 {
			cleanExitCount++
			if a.shouldStartExitedContainer(ctx, docker, c, cfg, dueJobs) {
				exitedFiltered = append(exitedFiltered, c)
				logInfof("monitor: queueing exited container=%s action=start", name)
				a.enqueueJob(actionJob{app: a, container: c, reason: "exited", action: "start"})
			} else {
				logDebugf("monitor: no start-exited job/filter match container=%s — skipping", name)
			}
		} else if a.processCrashedContainer(ctx, docker, hostID, c, cfg, jobs, dueJobIDs, exitCode) {
			exitedFiltered = append(exitedFiltered, c)
		}
	}
	a.mu.Lock()
	a.totalExited = cleanExitCount
	a.mu.Unlock()
	return exitedFiltered
}

func (a *app) fetchStartingContainers(ctx context.Context, docker dockerClient) []containertypes.Summary {
	var starting []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		starting, err = docker.StartingContainers(ctx)
		return err
	}); err != nil {
		logErrorf("failed to query starting containers: %v", err)
	}
	return starting
}

// finalizeState builds the problematic container set, detects recoveries, updates
// shared state, and returns the list of just-recovered containers for notification.
func (a *app) finalizeState(filtered, exitedFiltered, restarting, starting []containertypes.Summary) ([]recoveredContainer, map[string]bool) {
	problematic := make(map[string]bool, len(filtered)+len(exitedFiltered)+len(restarting))
	for _, c := range filtered {
		problematic[containerName(c)] = true
	}
	for _, c := range exitedFiltered {
		problematic[containerName(c)] = true
	}
	for _, c := range restarting {
		problematic[containerName(c)] = true
	}

	const defaultStableScansRequired = 3

	var justRecovered []recoveredContainer
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range starting {
		name := containerName(c)
		if s := a.cState[name]; s != nil && s.cycle > 0 {
			problematic[name] = true
		}
	}
	for name, s := range a.cState {
		if s.exhausted {
			continue
		}
		if problematic[name] {
			// Container is still problematic — reset any accumulated stability count.
			s.stableScans = 0
			continue
		}
		if !s.deadline.IsZero() && s.deadline.After(time.Now()) {
			// Still inside the post-action stabilisation window (success path).
			continue
		}
		if s.lastFailed {
			// Last action failed: require several consecutive stable scans before
			// declaring recovery. This prevents a brief "running" window during an
			// OCI shim timeout from being misread as a genuine recovery.
			required := s.stableScansRequired
			if required <= 0 {
				required = defaultStableScansRequired
			}
			s.stableScans++
			if s.stableScans < required {
				logInfof("monitor: container=%s stable for %d/%d scans — waiting before recovery",
					name, s.stableScans, required)
				continue
			}
		}
		logInfof("monitor: container recovered container=%s — resetting retry state", name)
		justRecovered = append(justRecovered, recoveredContainer{
			name:          name,
			notifications: a.lastJobNotifications[name],
			ruleName:      a.lastJobRuleName[name],
		})
		delete(a.cState, name)
		delete(a.lastJobNotifications, name)
		delete(a.lastJobRuleName, name)
	}
	a.unhealthy = filtered
	a.exited = exitedFiltered
	a.restarting = restarting
	a.lastScan = time.Now().UTC()
	return justRecovered, problematic
}

// matchingJobID returns the ID of the first enabled job that matches the container.
func matchingJobID(name string, labels map[string]string, jobs []config.Job) string {
	for _, r := range jobs {
		if r.Enabled && jobMatchesServiceSnapshot(name, labels, nil, r) {
			return r.ID
		}
	}
	return ""
}

func (a *app) collectHealthPulseTargets(allCtrs []containertypes.Summary, jobs []config.Job, dueJobIDs map[string]bool, problematic map[string]bool) []containertypes.Summary {
	var targets []containertypes.Summary
	for _, c := range allCtrs {
		name := containerName(c)
		if c.State != "running" || a.isSelfContainer(c) || problematic[name] {
			continue
		}
		jobID := matchingJobID(name, c.Labels, jobs)
		if jobID == "" || !dueJobIDs[jobID] {
			continue
		}
		targets = append(targets, c)
	}
	return targets
}

func (a *app) recordHealthPulses(ctx context.Context, docker dockerClient, jobs []config.Job, dueJobIDs map[string]bool, problematic map[string]bool) {
	var allCtrs []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		allCtrs, err = docker.AllContainers(ctx)
		return err
	}); err != nil {
		logWarnf("failed to query containers for health pulse: %v", err)
		return
	}
	a.mu.Lock()
	for _, c := range allCtrs {
		if c.ID != "" {
			a.knownNames[c.ID] = containerName(c)
		}
	}
	a.mu.Unlock()
	for _, c := range a.collectHealthPulseTargets(allCtrs, jobs, dueJobIDs, problematic) {
		a.logActionHistory(c, "health check", "healthy", 0, "healthy", "", "", 0)
	}
}

// jobHealthCheckMode returns the health-check mode for a job.
func jobHealthCheckMode(j config.Job) healthCheckMode {
	switch {
	case j.HealthCheckScript != "":
		return healthCheckScript
	default:
		return healthCheckNative
	}
}

// jobsForHost returns jobs that apply to the given hostID — jobs with empty
// DockerHostIDs match every host; jobs with a non-empty slice match only if
// hostID appears in that slice.
func jobsForHost(jobs []config.Job, hostID string) []config.Job {
	out := make([]config.Job, 0, len(jobs))
	for _, j := range jobs {
		if len(j.DockerHostIDs) == 0 || slices.Contains(j.DockerHostIDs, hostID) {
			out = append(out, j)
		}
	}
	return out
}

// scanHost scans a single Docker host and returns the problematic container sets.
func (a *app) scanHost(ctx context.Context, hostID string, docker dockerClient, cfg config.Config, allJobs []config.Job, dueJobs []config.Job, dueJobIDs map[string]bool) (filtered, exitedFiltered, restarting, starting []containertypes.Summary) {
	jobs := jobsForHost(allJobs, hostID)
	dueForHost := make(map[string]bool, len(dueJobIDs))
	for id := range dueJobIDs {
		for _, j := range jobs {
			if j.ID == id {
				dueForHost[id] = true
				break
			}
		}
	}

	// Group jobs by health-check mode for extensible dispatch.
	// To add a new health-check type: add a constant above, add a case in
	// jobHealthCheckMode, and add a case in the switch below.
	jobsByMode := make(map[healthCheckMode][]config.Job, 2)
	for _, j := range jobs {
		m := jobHealthCheckMode(j)
		jobsByMode[m] = append(jobsByMode[m], j)
	}

	// Native health checks require fetching Docker's unhealthy container list first.
	var unhealthyCtrs []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		unhealthyCtrs, err = docker.UnhealthyContainers(ctx)
		return err
	}); err != nil {
		logErrorf("docker-hosts: failed to query unhealthy containers host=%s: %v", hostID, err)
		return
	}

	logInfof("monitor: scan start host=%s unhealthy=%d totalJobs=%d dueJobs=%d scriptHealthJobs=%d",
		hostID, len(unhealthyCtrs), len(jobs), len(dueForHost), len(jobsByMode[healthCheckScript]))

	filtered = a.processUnhealthyContainers(ctx, docker, hostID, unhealthyCtrs, cfg, jobsByMode[healthCheckNative], dueForHost)
	var restartingMatched []containertypes.Summary
	restarting, restartingMatched = a.processRestartingContainers(ctx, docker, hostID, cfg, jobs, dueForHost)
	filtered = append(filtered, restartingMatched...)
	exitedFiltered = a.processExitedContainers(ctx, docker, hostID, cfg, jobs, dueJobs, dueForHost)
	starting = a.fetchStartingContainers(ctx, docker)

	// Dispatch non-native health-check modes — add new cases here as new modes are introduced.
	for mode, modeJobs := range jobsByMode {
		switch mode {
		case healthCheckNative:
			// Already handled above via processUnhealthyContainers.
		case healthCheckScript:
			unhealthy := a.processScriptHealthJobs(ctx, docker, hostID, cfg, modeJobs, dueForHost)
			filtered = append(filtered, unhealthy...)
		}
	}
	return
}

// processScriptHealthJobs handles jobs whose health is determined by a bash
// script rather than Docker's built-in health status. For each running
// container that matches a script-health job's filter, the script is executed;
// exit 0 means healthy (no action), non-zero exit means unhealthy (enqueue action).
func (a *app) processScriptHealthJobs(ctx context.Context, docker dockerClient, hostID string, cfg config.Config, jobs []config.Job, dueJobIDs map[string]bool) []containertypes.Summary {
	var running []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		running, err = docker.RunningContainers(ctx)
		return err
	}); err != nil {
		logErrorf("docker-hosts: failed to query running containers host=%s: %v", hostID, err)
		return nil
	}

	var unhealthy []containertypes.Summary
	for _, c := range running {
		name := containerName(c)
		if a.isSelfContainer(c) {
			continue
		}
		for _, job := range jobs {
			if !job.Enabled {
				continue
			}
			matches, err := a.matchesJob(ctx, docker, c, job)
			if err != nil || !matches {
				continue
			}

			timeout := job.ActionTimeoutSeconds
			if timeout < 5 {
				timeout = 30
			}
			hcCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			_, hcErr := a.executeRunScript(hcCtx, job.HealthCheckScript, cfg, c, name, false)
			cancel()

			if hcErr == nil {
				// Exit 0 — container is healthy, no action needed.
				continue
			}
			// Distinguish a non-zero exit (unhealthy) from a script execution error.
			var exitErr *exec.ExitError
			if !errors.As(hcErr, &exitErr) {
				logWarnf("monitor: health-check script error container=%s job=%s: %v", name, job.ID, hcErr)
				continue
			}

			logInfof("monitor: health-check script unhealthy container=%s id=%.12s jobID=%s jobName=%s", name, c.ID, job.ID, job.Name)
			unhealthy = append(unhealthy, c)

			if !dueJobIDs[job.ID] {
				logDebugf("monitor: job not due container=%s jobID=%s", name, job.ID)
				break
			}
			j := job // capture before taking address — range var may be reused in older Go
			sel := jobSelection{
				action:        job.Action,
				script:        job.Script,
				notifications: job.Notifications,
				jobID:         job.ID,
				jobName:       job.Name,
				job:           &j,
				ok:            true,
			}
			if a.enqueueIfReady(c, "unhealthy", sel) && a.shouldNotify(name) {
				if err := a.sendJobNotifications("unhealthy detected", notifData{
					ContainerName: name,
					RuleName:      job.Name,
					Reason:        "unhealthy",
					MaxRetries:    effectiveRetryCount(&j),
				}, job.Notifications); err != nil {
					logWarnf("notify: job send failed event=unhealthy container=%s err=%v", name, err)
				}
			}
			break // first matching script job wins per container
		}
	}
	return unhealthy
}

func (a *app) scanOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := a.getConfig()
	allJobs := a.getJobs()
	dueJobs, dueJobIDs := a.computeDueJobs(allJobs)

	// Scan primary host + all extra hosts, merging results.
	var allFiltered, allExited, allRestarting, allStarting []containertypes.Summary
	f, e, r, s := a.scanHost(ctx, "local", a.docker, cfg, allJobs, dueJobs, dueJobIDs)
	allFiltered = append(allFiltered, f...)
	allExited = append(allExited, e...)
	allRestarting = append(allRestarting, r...)
	allStarting = append(allStarting, s...)

	for _, h := range a.getExtraClients() {
		if !h.profile.Enabled {
			continue
		}

		// Respect per-host monitor interval (0 = always scan on every cycle).
		if interval := h.profile.MonitorIntervalSeconds; interval > 0 {
			a.mu.RLock()
			lastScan := a.dockerHostLastScan[h.profile.ID]
			a.mu.RUnlock()
			if time.Since(lastScan) < time.Duration(interval)*time.Second {
				continue
			}
		}
		a.mu.Lock()
		a.dockerHostLastScan[h.profile.ID] = time.Now()
		a.mu.Unlock()

		// Ping using per-host timeout, falling back to global or 5s.
		pingTimeout := h.profile.PingTimeoutSeconds
		if pingTimeout <= 0 {
			if cfg.DockerPingTimeoutSeconds > 0 {
				pingTimeout = cfg.DockerPingTimeoutSeconds
			} else {
				pingTimeout = 5
			}
		}
		pingCtx, pingCancel := context.WithTimeout(ctx, time.Duration(pingTimeout)*time.Second)
		hostUp, _, _ := dockerhealth.PingHost(pingCtx, h.profile.Type, h.profile.Endpoint, pingTimeout, nil)
		pingCancel()

		a.mu.Lock()
		wasUp := a.dockerHostConnected[h.profile.ID]
		a.dockerHostConnected[h.profile.ID] = hostUp
		if hostUp {
			// Host is back — clear outage tracking.
			delete(a.dockerHostOfflineSince, h.profile.ID)
			if !wasUp {
				a.dockerHostNotified[h.profile.ID] = false
			}
		} else if wasUp {
			a.dockerHostOfflineSince[h.profile.ID] = time.Now()
		}
		offlineSince := a.dockerHostOfflineSince[h.profile.ID]
		alreadyNotified := a.dockerHostNotified[h.profile.ID]
		a.mu.Unlock()

		if hostUp && !wasUp {
			logInfof("docker-hosts: host came back online id=%s name=%s", h.profile.ID, h.profile.Name)
			a.sendDockerHostNotif(h.profile, false)
		} else if !hostUp && !alreadyNotified && !offlineSince.IsZero() {
			// Fire notification once the host has been offline for OfflineConfirmSeconds.
			confirmSecs := h.profile.OfflineConfirmSeconds
			if confirmSecs <= 0 {
				confirmSecs = 1800 // default: 30 minutes
			}
			if time.Since(offlineSince) >= time.Duration(confirmSecs)*time.Second {
				logWarnf("docker-hosts: host offline >%ds id=%s name=%s", confirmSecs, h.profile.ID, h.profile.Name)
				a.sendDockerHostNotif(h.profile, true)
				a.mu.Lock()
				a.dockerHostNotified[h.profile.ID] = true
				a.mu.Unlock()
			}
		}

		if !hostUp {
			logWarnf("docker-hosts: skipping scan for offline host id=%s name=%s", h.profile.ID, h.profile.Name)
			continue
		}

		f, e, r, s = a.scanHost(ctx, h.profile.ID, h.client, cfg, allJobs, dueJobs, dueJobIDs)
		allFiltered = append(allFiltered, f...)
		allExited = append(allExited, e...)
		allRestarting = append(allRestarting, r...)
		allStarting = append(allStarting, s...)
	}

	justRecovered, problematic := a.finalizeState(allFiltered, allExited, allRestarting, allStarting)

	for _, ev := range justRecovered {
		c := containertypes.Summary{Names: []string{"/" + ev.name}}
		a.logActionHistory(c, "container recovered", "recovered", 0, "recovered", "", "", 0)
		msg := fmt.Sprintf("Container %s recovered and is healthy", ev.name)
		if a.shouldNotify(ev.name) {
			if err := a.sendNotification("container recovered", msg); err != nil {
				logWarnf("notify: send failed event=recovered container=%s err=%v", ev.name, err)
			}
			if err := a.sendJobNotifications("container recovered", notifData{
				ContainerName: ev.name,
				RuleName:      ev.ruleName,
			}, ev.notifications); err != nil {
				logWarnf("notify: job send failed event=recovered container=%s err=%v", ev.name, err)
			}
		}
	}

	// Record health pulses for each host, scoped to jobs that apply to that host
	// so a job pinned to host X does not produce a healthy pulse on host Y.
	a.recordHealthPulses(ctx, a.docker, jobsForHost(allJobs, "local"), dueJobIDs, problematic)
	for _, h := range a.getExtraClients() {
		a.recordHealthPulses(ctx, h.client, jobsForHost(allJobs, h.profile.ID), dueJobIDs, problematic)
	}

	logInfof("monitor: scan complete unhealthyQueued=%d exitedQueued=%d hosts=%d",
		len(allFiltered), len(allExited), 1+len(a.getExtraClients()))
}

func (a *app) selectActionForContainer(ctx context.Context, docker dockerClient, c containertypes.Summary, cfg config.Config, jobs []config.Job) jobSelection {
	name := containerName(c)
	hasEnabledJobs := false
	for i, r := range jobs {
		if !r.Enabled {
			logDebugf("job: skip disabled jobID=%s jobName=%s container=%s", r.ID, r.Name, name)
			continue
		}
		hasEnabledJobs = true
		logDebugf("job: evaluating jobID=%s jobName=%s container=%s action=%s nameFilter=%q labelFilter=%q envFilter=%q notifications=%d",
			r.ID, r.Name, name, r.Action,
			r.ContainerNameFilter, r.ContainerLabelFilter, r.ContainerEnvVarFilter, len(r.Notifications))
		ok, err := a.matchesJob(ctx, docker, c, r)
		if err != nil {
			logWarnf("job: match error jobID=%s jobName=%s container=%s err=%v", r.ID, r.Name, name, err)
			continue
		}
		if ok {
			logInfof("job: matched jobID=%s jobName=%s container=%s action=%s notifications=%d",
				r.ID, r.Name, name, r.Action, len(r.Notifications))
			return jobSelection{
				action:        r.Action,
				script:        r.Script,
				notifications: r.Notifications,
				jobID:         r.ID,
				jobName:       r.Name,
				job:           &jobs[i],
				ok:            true,
			}
		}
		logDebugf("job: no match jobID=%s jobName=%s container=%s", r.ID, r.Name, name)
	}

	if !hasEnabledJobs && hasAnyFilters(cfg) {
		logDebugf("job: no enabled jobs — checking global filters container=%s nameFilter=%q labelFilter=%q envFilter=%q",
			name, cfg.ContainerNameFilter, cfg.ContainerLabelFilter, cfg.ContainerEnvVarFilter)
		if a.matchesFilters(ctx, docker, c, cfg) {
			logInfof("job: matched global-config container=%s action=%s", name, cfg.Action)
			return jobSelection{action: cfg.Action, jobName: "global-config", ok: true}
		}
		logDebugf("job: global filter no match container=%s", name)
	}

	return jobSelection{}
}

func (a *app) matchesJob(ctx context.Context, docker dockerClient, c containertypes.Summary, r config.Job) (bool, error) {
	name := containerName(c)

	if r.ContainerNameFilter != "" {
		allowed := splitCSV(r.ContainerNameFilter)
		ok := false
		for _, x := range allowed {
			if strings.Contains(name, x) {
				ok = true
				break
			}
		}
		logDebugf("job: name-filter jobID=%s container=%s filter=%q containerName=%q match=%t",
			r.ID, name, r.ContainerNameFilter, name, ok)
		if !ok {
			return false, nil
		}
	}

	if r.ContainerLabelFilter != "" {
		ok := false
		for k, v := range c.Labels {
			if strings.Contains(k+"="+v, r.ContainerLabelFilter) {
				ok = true
				break
			}
		}
		logDebugf("job: label-filter jobID=%s container=%s filter=%q match=%t",
			r.ID, name, r.ContainerLabelFilter, ok)
		if !ok {
			return false, nil
		}
	}

	if r.ContainerEnvVarFilter != "" {
		inspected, err := docker.InspectContainer(ctx, c.ID)
		if err != nil {
			return false, err
		}
		ok := dockerhealth.ContainerHasEnvVar(inspected, r.ContainerEnvVarFilter)
		logDebugf("job: env-filter jobID=%s container=%s filter=%q match=%t",
			r.ID, name, r.ContainerEnvVarFilter, ok)
		if !ok {
			return false, nil
		}
	}

	return true, nil
}

func (a *app) shouldStartExitedContainer(ctx context.Context, docker dockerClient, c containertypes.Summary, cfg config.Config, jobs []config.Job) bool {
	for _, r := range jobs {
		if !r.Enabled || !r.StartExited {
			continue
		}
		matched, err := a.matchesJob(ctx, docker, c, r)
		if err != nil {
			logWarnf("job match error (%s): %v", r.ID, err)
			continue
		}
		if matched {
			logInfof("monitor: exited start matched by job jobID=%s container=%s", r.ID, containerName(c))
			return true
		}
	}

	if cfg.StartExited && hasAnyFilters(cfg) {
		logDebugf("monitor: exited start checking global filters container=%s", containerName(c))
		return a.matchesFilters(ctx, docker, c, cfg)
	}

	return false
}

func (a *app) matchesFilters(ctx context.Context, docker dockerClient, c containertypes.Summary, cfg config.Config) bool {
	name := containerName(c)

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
		for k, v := range c.Labels {
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
		inspected, err := docker.InspectContainer(ctx, c.ID)
		if err != nil {
			logWarnf("inspect failed for env filter on container %s: %v", name, err)
			return false
		}
		if !dockerhealth.ContainerHasEnvVar(inspected, cfg.ContainerEnvVarFilter) {
			return false
		}
	}

	return true
}

func (a *app) isSelfContainer(c containertypes.Summary) bool {
	return (a.selfID != "" && strings.HasPrefix(c.ID, a.selfID)) ||
		(a.selfName != "" && containerName(c) == a.selfName)
}

func detectSelfIdentity(a *app) (string, string) {
	raw, err := os.ReadFile("/etc/hostname")
	if err != nil {
		return "", ""
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return "", ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspected, err := a.docker.InspectContainer(ctx, id)
	if err != nil {
		return id, ""
	}
	return id, dockerhealth.ContainerName(inspected)
}
