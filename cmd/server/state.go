package main

import (
	"strings"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/history"
)

// containerActionState consolidates per-container action-cycle tracking.
// All fields are accessed under a.mu.
type containerActionState struct {
	cycle               int       // number of action attempts made
	exhausted           bool      // true once the retry limit has been hit
	deadline            time.Time // earliest time the monitor may act again (post-action wait)
	lastFailed          bool      // true when the most recent action returned an error
	stableScans         int       // consecutive stable scans since lastFailed — used for recovery gate
	stableScansRequired int       // job-configured threshold; 0 means use the default (3)
}

// ensureContainerState returns the state bucket for name, creating it if absent.
// Callers MUST hold a.mu (write-lock).
func (a *app) ensureContainerState(name string) *containerActionState {
	s := a.cState[name]
	if s == nil {
		s = &containerActionState{}
		a.cState[name] = s
	}
	return s
}

func (a *app) logActionHistory(c containertypes.Summary, reason, action string, attempt int, status, errMsg, output string, duration time.Duration) {
	if a.history == nil {
		return
	}
	entry := history.Entry{
		Timestamp:     time.Now().UTC(),
		ContainerID:   c.ID,
		ContainerName: containerName(c),
		Reason:        reason,
		Action:        action,
		Attempt:       attempt,
		Status:        status,
		Error:         errMsg,
		Output:        strings.TrimSpace(output),
		DurationMs:    duration.Milliseconds(),
	}
	if err := a.history.Append(entry); err != nil {
		logErrorf("failed to append action history: %v", err)
	}
}

func (a *app) incrementActionCycle(containerID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.ensureContainerState(containerID)
	s.cycle++
	return s.cycle
}

func (a *app) isRetriesExhausted(containerID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s := a.cState[containerID]
	return s != nil && s.exhausted
}

// shouldNotify gates per-container notification bursts. Keyed by container NAME
// (not Docker ID) — a renamed container gets a fresh cooldown window, which is
// acceptable: the name is what operators see, so a rename usually signals a
// replacement and a fresh notification is correct.
func (a *app) shouldNotify(name string) bool {
	cfg := a.getConfig()
	if cfg.NotificationCooldownSeconds <= 0 {
		return true
	}
	cooldown := time.Duration(cfg.NotificationCooldownSeconds) * time.Second

	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if last, ok := a.lastNotified[name]; ok {
		elapsed := now.Sub(last)
		if elapsed < cooldown {
			remaining := (cooldown - elapsed).Round(time.Second)
			logInfof("cooldown: notification suppressed name=%s elapsed=%s cooldown=%s remaining=%s",
				name, elapsed.Round(time.Second), cooldown, remaining)
			return false
		}
	}
	a.lastNotified[name] = now
	return true
}

func (a *app) setRetriesExhausted(containerID string, v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureContainerState(containerID).exhausted = v
}

func (a *app) claimActiveJob(containerID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeJobs[containerID] {
		return false
	}
	a.activeJobs[containerID] = true
	return true
}

func (a *app) releaseActiveJob(containerID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.activeJobs, containerID)
}

func (a *app) setLastActionFailed(containerID string, failed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.ensureContainerState(containerID)
	s.lastFailed = failed
	if failed {
		s.stableScans = 0
	}
}

func (a *app) setLastJobScan(jobID string, t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastJobScan != nil {
		a.lastJobScan[jobID] = t
	}
}

func (a *app) recordJobTracking(name string, notifications []string, ruleName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastJobNotifications != nil {
		a.lastJobNotifications[name] = notifications
	}
	if a.lastJobRuleName != nil {
		a.lastJobRuleName[name] = ruleName
	}
}

func (a *app) setPostActionDeadline(containerID string, deadline time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureContainerState(containerID).deadline = deadline
}

func (a *app) postActionWaitRemaining(containerID string) time.Duration {
	a.mu.RLock()
	s := a.cState[containerID]
	a.mu.RUnlock()
	if s == nil || s.deadline.IsZero() {
		return 0
	}
	remaining := time.Until(s.deadline)
	if remaining <= 0 {
		a.mu.Lock()
		if cs := a.cState[containerID]; cs != nil {
			cs.deadline = time.Time{}
		}
		a.mu.Unlock()
		return 0
	}
	return remaining
}

func (a *app) resetActionCooldown(containerID string) {
	a.mu.Lock()
	delete(a.lastNotified, containerID)
	delete(a.cState, containerID)
	delete(a.activeJobs, containerID)
	delete(a.lastJobRuleName, containerID)
	a.mu.Unlock()
	logInfof("cooldown: reset containerID=%.12s", containerID)
}

func (a *app) setStableScansRequired(name string, n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureContainerState(name).stableScansRequired = n
}

func (a *app) containerNameByID(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, c := range a.unhealthy {
		if c.ID == id {
			return containerName(c)
		}
	}
	for _, c := range a.exited {
		if c.ID == id {
			return containerName(c)
		}
	}
	if name, ok := a.knownNames[id]; ok {
		return name
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
