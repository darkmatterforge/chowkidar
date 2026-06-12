package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
)

func resolveJobSettings(j actionJob, cfg config.Config) (retryCount, actionTimeout int) {
	if j.jobID == "" {
		// No matching job (start-exited or manual action) — single attempt only.
		// Retries for these cases should be handled by defining a job rule.
		return 1, cfg.ActionTimeoutSeconds
	}
	// retryCount == 0 means unlimited; keep it as-is.
	at := j.actionTimeoutSeconds
	if at < 1 {
		at = cfg.ActionTimeoutSeconds
	}
	return j.retryCount, at
}

func (a *app) notifyRetriesExhausted(j actionJob, name, action string, cycle, retryCount int, failed bool) {
	a.setRetriesExhausted(name, true)
	a.logActionHistory(j.container, j.reason, action, cycle, "exhausted", "monitoring paused — max retries reached", "", 0)
	event := "retry limit reached"
	msg := fmt.Sprintf("Container %s monitoring paused after %d cycles (%s)", name, cycle, j.reason)
	if failed {
		event = "action failed"
		msg = fmt.Sprintf("Container %s action %s failed after %d cycles (%s)", name, action, cycle, j.reason)
	}
	if a.shouldNotify(name) {
		if err := a.sendNotification(event, msg); err != nil {
			logWarnf("notify: send failed event=%s container=%s err=%v", event, name, err)
		}
		if err := a.sendJobNotifications(event, notifData{
			ContainerName: name,
			RuleName:      j.jobName,
			Action:        action,
			Reason:        j.reason,
			Cycle:         cycle,
			MaxRetries:    retryCount,
		}, j.notifications); err != nil {
			logWarnf("notify: job send failed event=%s container=%s err=%v", event, name, err)
		}
	}
}

func (j actionJob) Run(ctx context.Context) {
	name := containerName(j.container)
	cfg := j.app.getConfig()
	action := strings.ToLower(strings.TrimSpace(j.action))
	if action == "" {
		action = strings.ToLower(cfg.Action)
	}

	retryCount, actionTimeout := resolveJobSettings(j, cfg)
	postWait := time.Duration(j.postActionWaitSeconds) * time.Second

	source := "global-config"
	if j.jobID != "" {
		source = fmt.Sprintf("job:%s(%s)", j.jobName, j.jobID)
	}

	if j.force {
		logInfof("job: force-run container=%s action=%s source=%s", name, action, source)
		j.app.resetActionCooldown(name)
	}

	if !j.force {
		if !j.app.claimActiveJob(name) {
			logInfof("job: skipping container=%s reason=job-already-running source=%s", name, source)
			return
		}
		defer j.app.releaseActiveJob(name)

		// Give a force-cancel maintenance window a way to interrupt this job
		// mid-flight: derive a cancellable context and register it under the
		// container name so checkMaintenanceTransitions can reach in and stop
		// an in-flight Docker call (executeAction threads ctx into its own
		// context.WithTimeout calls, so cancellation propagates straight
		// through) the moment such a window opens.
		var jobCancel context.CancelFunc
		ctx, jobCancel = context.WithCancel(ctx)
		j.app.registerActiveJobCancel(name, j.jobID, jobCancel)
		defer j.app.unregisterActiveJobCancel(name)
		defer jobCancel()

		if j.app.isRetriesExhausted(name) {
			logInfof("job: skipping container=%s reason=retries-exhausted source=%s", name, source)
			return
		}

		// Queue-lag guard: re-check post-action wait after claiming the slot
		if remaining := j.app.postActionWaitRemaining(name); remaining > 0 {
			logInfof("job: skipping container=%s reason=post-action-wait remaining=%s source=%s",
				name, remaining.Round(time.Second), source)
			j.app.logActionHistory(j.container, j.reason, action, 0, "skipped", "cooldown active", "", 0)
			return
		}

		// Maintenance-window guard: a window may have opened in the moments
		// between this action being dispatched and the worker claiming it.
		// Re-check now — if its OnStart policy doesn't allow queued actions
		// to proceed, bail before making any Docker call. (force-cancel gets
		// its own mid-flight interruption below; this just catches the case
		// where the call hasn't started yet.)
		if onStart := j.app.maintenanceOnStartFor(j.jobID); onStart == config.MaintenanceOnStartCancelQueued || onStart == config.MaintenanceOnStartForceCancel {
			logInfof("job: skipping container=%s reason=maintenance-window-started onStart=%s source=%s", name, onStart, source)
			j.app.logActionHistory(j.container, j.reason, action, 0, "skipped", "maintenance window started", "", 0)
			return
		}
	}

	// Increment cycle BEFORE acting so history reflects the correct attempt number
	cycle := j.app.incrementActionCycle(name)
	logInfof("job: start container=%s reason=%s action=%s cycle=%d/%d actionTimeout=%ds postWait=%s source=%s",
		name, j.reason, action, cycle, retryCount, actionTimeout, postWait, source)

	// Notify each retry attempt as it starts.
	if err := j.app.sendJobNotifications("retrying", notifData{
		ContainerName: name,
		RuleName:      j.jobName,
		Action:        action,
		Reason:        j.reason,
		Cycle:         cycle,
		MaxRetries:    retryCount,
	}, j.notifications); err != nil {
		logWarnf("notify: job send failed event=retrying container=%s err=%v", name, err)
	}

	started := time.Now()
	docker := j.docker
	if docker == nil {
		docker = j.app.docker
	}
	scriptOutput, err := j.app.executeAction(ctx, docker, action, j.script, j.container, actionTimeout)
	duration := time.Since(started)

	if err == nil {
		// Action succeeded — apply post-action wait to give the container time to stabilise
		// before the next scan checks it again.
		if postWait > 0 {
			j.app.setPostActionDeadline(name, time.Now().Add(postWait))
			logInfof("job: post-action wait scheduled container=%s window=%s source=%s", name, postWait, source)
			if err := j.app.sendJobNotifications("cooldown", notifData{
				ContainerName: name,
				RuleName:      j.jobName,
				Cooldown:      j.postActionWaitSeconds,
			}, j.notifications); err != nil {
				logWarnf("notify: job send failed event=cooldown container=%s err=%v", name, err)
			}
		}
		if scriptOutput != "" {
			logInfof("job: script output container=%s:\n%s", name, scriptOutput)
		}
		logInfof("job: success container=%s action=%s cycle=%d/%d duration=%s source=%s",
			name, action, cycle, retryCount, duration.Round(time.Millisecond), source)
		j.app.logActionHistory(j.container, j.reason, action, cycle, "success", "", scriptOutput, duration)
		j.app.setLastActionFailed(name, false)
		if j.force {
			// Manual action succeeded — reset cycle so auto-monitoring resumes cleanly.
			j.app.resetActionCooldown(name)
			return
		}
		if retryCount > 0 && cycle >= retryCount {
			logErrorf("job: retries exhausted after api-success container=%s action=%s cycle=%d/%d source=%s",
				name, action, cycle, retryCount, source)
			j.app.notifyRetriesExhausted(j, name, action, cycle, retryCount, false)
			return
		}
		if j.jobID != "" {
			j.app.recordJobTracking(name, j.notifications, j.jobName)
		}
		return
	}

	logWarnf("job: failed container=%s action=%s cycle=%d/%d duration=%s err=%v source=%s",
		name, action, cycle, retryCount, duration.Round(time.Millisecond), err, source)
	j.app.logActionHistory(j.container, j.reason, action, cycle, "failed", err.Error(), scriptOutput, duration)
	if !j.force {
		// Mark last action as failed so the scan doesn't misread a brief "running"
		// window (e.g. OCI shim timeout) as a genuine recovery.
		j.app.setLastActionFailed(name, true)
		// Reset the job-scan timestamp so the NEXT retry fires monitorIntervalSeconds
		// from NOW (when this attempt finished), not from when the scan that queued
		// this action originally ran. Without this, a long-running failed action can
		// cause the queued-but-waiting next action to start immediately after release,
		// making retries appear much faster than the configured interval.
		if j.jobID != "" {
			j.app.setLastJobScan(j.jobID, time.Now())
		}
	}
	if !j.force && retryCount > 0 && cycle >= retryCount {
		logErrorf("job: retries exhausted container=%s action=%s cycle=%d/%d source=%s",
			name, action, cycle, retryCount, source)
		j.app.notifyRetriesExhausted(j, name, action, cycle, retryCount, true)
	}
}

// applyMatchedJobSettings copies per-job overrides from matched into job.
func applyMatchedJobSettings(job *actionJob, matched *config.Job) {
	if matched == nil {
		return
	}
	job.retryCount = effectiveRetryCount(matched)
	if matched.ActionTimeoutSeconds > 0 {
		job.actionTimeoutSeconds = matched.ActionTimeoutSeconds
	}

	job.postActionWaitSeconds = matched.PostActionWaitSeconds
	if job.postActionWaitSeconds <= 0 {
		job.postActionWaitSeconds = matched.MonitorIntervalSeconds
	}
}

// allowedInterpreters is the set of shell interpreters permitted in script shebangs.
// Only these paths may be used as the exec.Command executable; anything else falls
// back to /bin/sh so a crafted shebang cannot invoke arbitrary binaries.
var allowedInterpreters = map[string]bool{
	"/bin/sh":             true,
	"/bin/bash":           true,
	"/usr/bin/sh":         true,
	"/usr/bin/bash":       true,
	"/usr/local/bin/bash": true,
	"/usr/local/bin/sh":   true,
}

// resolveScriptInterpreter reads the shebang line from a script and returns
// the interpreter path if it exists on the system and is in the allowlist,
// falling back to /bin/sh otherwise.
// This prevents "no such file or directory" when e.g. bash is not installed,
// and prevents a crafted shebang from executing arbitrary binaries.
func resolveScriptInterpreter(script string) string {
	const fallback = "/bin/sh"
	trimmed := strings.TrimSpace(script)
	if !strings.HasPrefix(trimmed, "#!") {
		return fallback
	}
	firstLine, _, _ := strings.Cut(trimmed, "\n")
	interpreter := strings.TrimSpace(strings.TrimPrefix(firstLine, "#!"))
	// Handle cases like "#!/usr/bin/env bash" — just take the first word
	parts := strings.Fields(interpreter)
	if len(parts) == 0 {
		return fallback
	}
	// If the shebang is "/usr/bin/env bash", resolve the named shell via LookPath
	candidate := parts[0]
	if candidate == "/usr/bin/env" && len(parts) > 1 {
		shellName := parts[1]
		// Only allow env-resolved shells that are in our allowlist by name
		if shellName != "bash" && shellName != "sh" {
			return fallback
		}
		if resolved, err := exec.LookPath(shellName); err == nil {
			if allowedInterpreters[resolved] {
				return resolved
			}
		}
		return fallback
	}
	// Direct path: must be in the allowlist and actually exist
	if allowedInterpreters[candidate] {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return fallback
}

// executeRunScript runs the inline or path-based script and returns its
// combined stdout+stderr output alongside any error.
func (a *app) executeRunScript(ctx context.Context, script string, cfg config.Config, c containertypes.Summary, name string, dryRun bool) (string, error) {
	if strings.TrimSpace(script) != "" {
		tmpFile, err := os.CreateTemp("", "chowkidar-*.sh")
		if err != nil {
			return "", fmt.Errorf("create temp script: %w", err)
		}
		tmpPath := tmpFile.Name()
		defer func() { _ = os.Remove(tmpPath) }()
		if _, err := tmpFile.WriteString(script); err != nil {
			_ = tmpFile.Close()
			return "", fmt.Errorf("write temp script: %w", err)
		}
		_ = tmpFile.Close()
		if err := os.Chmod(tmpPath, 0o400); err != nil {
			return "", fmt.Errorf("chmod temp script: %w", err)
		}
		// Invoke via sh so the script runs on noexec-mounted filesystems and
		// honours shebang lines regardless of kernel exec permissions.
		// tmpPath is server-generated, not user-controlled.
		cmd := exec.CommandContext(ctx, "sh", tmpPath, c.ID, name) // #nosec G204
		// Inherit environment and inject DRY_RUN when this is a test run from the
		// UI so scripts can skip destructive steps safely.
		cmd.Env = append(os.Environ(), "CHOWKIDAR=1")
		if dryRun || c.ID == "dry-run-test" {
			cmd.Env = append(cmd.Env, "DRY_RUN=1")
		}
		out, err := cmd.CombinedOutput()
		output := strings.TrimSpace(string(out))
		if err != nil {
			return output, fmt.Errorf("script failed: %w", err)
		}
		return output, nil
	}
	if strings.TrimSpace(cfg.RunScriptPath) == "" {
		return "", fmt.Errorf("run-script action selected but RUN_SCRIPT_PATH is empty")
	}
	if !a.isScriptAllowed(cfg.RunScriptPath) {
		return "", fmt.Errorf("run-script path is not in scripts allowlist")
	}
	cmd := exec.CommandContext(ctx, cfg.RunScriptPath, c.ID, name)
	out2, err2 := cmd.CombinedOutput()
	output2 := strings.TrimSpace(string(out2))
	if err2 != nil {
		return output2, fmt.Errorf("script failed: %w", err2)
	}
	return output2, nil
}

// executeAction runs the action for a container and returns any script output
// (non-empty only for run-script actions) alongside the error.
// scriptTimeoutSeconds overrides actionTimeoutSeconds specifically for run-script;
func (a *app) executeAction(ctx context.Context, docker dockerClient, action string, script string, c containertypes.Summary, actionTimeoutSeconds int) (string, error) {
	cfg := a.getConfig()

	effectiveTimeout := func() time.Duration {
		sec := actionTimeoutSeconds
		if sec < 1 {
			sec = cfg.ActionTimeoutSeconds
		}
		return max(time.Duration(sec)*time.Second, 5*time.Second)
	}

	name := containerName(c)
	switch action {
	case "restart":
		actionCtx, cancel := context.WithTimeout(ctx, effectiveTimeout())
		defer cancel()
		return "", docker.RestartContainer(actionCtx, c.ID, 10)
	case "start":
		actionCtx, cancel := context.WithTimeout(ctx, effectiveTimeout())
		defer cancel()
		return "", docker.StartContainer(actionCtx, c.ID)
	case "stop":
		actionCtx, cancel := context.WithTimeout(ctx, effectiveTimeout())
		defer cancel()
		return "", docker.StopContainer(actionCtx, c.ID, 10)
	case "none":
		return "", nil
	case "run-script":
		scriptCtx, cancel := context.WithTimeout(ctx, effectiveTimeout())
		defer cancel()
		return a.executeRunScript(scriptCtx, script, cfg, c, name, false)
	default:
		return "", fmt.Errorf("unsupported action %q", action)
	}
}

func (a *app) isScriptAllowed(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	entries := a.getScriptEntries()
	if len(entries) == 0 {
		return false
	}
	for _, s := range entries {
		if !s.Enabled {
			continue
		}
		if strings.TrimSpace(s.Path) == path {
			return true
		}
	}
	return false
}
