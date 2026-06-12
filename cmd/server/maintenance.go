package main

import (
	"context"
	"time"

	"chowkidar/internal/config"
)

// maintenancePause is a snapshot of which jobs/hosts are paused for the
// current evaluation instant, computed once per scan cycle from the set of
// currently-active maintenance windows.
type maintenancePause struct {
	skipEntireScan bool
	pausedJobIDs   map[string]bool
	pausedHostIDs  map[string]bool
	active         []config.MaintenanceWindow

	// jobOnStart maps a paused job ID to the strongest OnStart behaviour
	// requested by any active window currently pausing it; localOnStart is
	// its counterpart for job-less actions, which always run against the
	// local Docker client (see onStartFor).
	jobOnStart   map[string]string
	localOnStart string
}

// computeMaintenancePause expands the currently-active maintenance windows
// into a concrete pause snapshot:
//   - a job ID listed directly on a window pauses just that job
//   - a Docker host ID pauses every job pinned to that host (via jobsForHost)
//     plus the host's own offline/connectivity monitoring
//   - the built-in "local" host additionally sets skipEntireScan, since the
//     whole app depends on the local Docker daemon
func (a *app) computeMaintenancePause(allJobs []config.Job, now time.Time) maintenancePause {
	active := config.ActiveMaintenanceWindows(a.getMaintenanceWindows(), now)
	p := maintenancePause{
		pausedJobIDs:  map[string]bool{},
		pausedHostIDs: map[string]bool{},
		jobOnStart:    map[string]string{},
		active:        active,
	}
	for _, w := range active {
		for _, id := range w.JobIDs {
			p.pausedJobIDs[id] = true
			p.jobOnStart[id] = strongerOnStart(p.jobOnStart[id], w.OnStart)
		}
		for _, hostID := range w.DockerHostIDs {
			p.pausedHostIDs[hostID] = true
			if hostID == "local" {
				p.skipEntireScan = true
				p.localOnStart = strongerOnStart(p.localOnStart, w.OnStart)
			}
			for _, j := range jobsForHost(allJobs, hostID) {
				p.pausedJobIDs[j.ID] = true
				p.jobOnStart[j.ID] = strongerOnStart(p.jobOnStart[j.ID], w.OnStart)
			}
		}
	}
	return p
}

// onStartRank orders OnStart behaviours from least to most aggressive, so that
// when multiple active windows pause the same job/host with different settings
// the strictest one governs — a force-cancel window should never be silently
// overridden by a co-active allow-finish window.
func onStartRank(v string) int {
	switch v {
	case config.MaintenanceOnStartForceCancel:
		return 2
	case config.MaintenanceOnStartCancelQueued:
		return 1
	default:
		return 0
	}
}

func strongerOnStart(a, b string) string {
	if onStartRank(b) > onStartRank(a) {
		return b
	}
	return a
}

// onStartFor returns the OnStart behaviour that applies to the given job (or,
// for job-less actions — which always run against the local Docker client —
// to the "local" host) under this pause snapshot. An empty string means the
// job/host isn't currently paused at all.
func (p maintenancePause) onStartFor(jobID string) string {
	if jobID == "" {
		return p.localOnStart
	}
	return p.jobOnStart[jobID]
}

// maintenanceOnStartFor re-resolves the current maintenance pause snapshot and
// returns the OnStart behaviour (if any) governing the given job. Used by
// actionJob.Run to catch a window that opened in the moments between an
// action being dispatched and the worker actually claiming it — the snapshot
// computed at dispatch time (in scanOnce) may already be stale by then.
func (a *app) maintenanceOnStartFor(jobID string) string {
	return a.computeMaintenancePause(a.getJobs(), time.Now()).onStartFor(jobID)
}

func (p maintenancePause) jobPaused(id string) bool {
	return p.pausedJobIDs[id]
}

func (p maintenancePause) hostPaused(id string) bool {
	return p.pausedHostIDs[id]
}

// filterPausedJobs returns jobs that are not currently paused under p.
func filterPausedJobs(jobs []config.Job, p maintenancePause) []config.Job {
	out := make([]config.Job, 0, len(jobs))
	for _, j := range jobs {
		if p.jobPaused(j.ID) {
			continue
		}
		out = append(out, j)
	}
	return out
}

// maintenanceTransition records the moment a window's active state flipped —
// "started" (services just paused) or "ended" (services just resumed) — so
// handleSystemAlerts can surface the full pause lifecycle (warned → paused →
// resumed) in the bell. Title is captured at detection time because by the
// time an "ended" transition is recorded, the window is no longer active.
type maintenanceTransition struct {
	WindowID string
	Title    string
	Kind     string // "started" | "ended"
	At       time.Time
}

// maintenanceTransitionRetention bounds how long start/end transition records
// are kept — by the time something is a day old, the windows list's "Last"
// column and the upcoming-reminder alerts already tell that story.
const maintenanceTransitionRetention = 24 * time.Hour

// checkMaintenanceTransitions diffs the set of currently-active windows
// against what was active on the previous scan cycle, recording a "started"
// entry for each newly-active window and an "ended" entry for each window that
// just stopped being active. Mirrors the online/offline transition-detection
// idiom in scanOnce (wasUp := a.dockerHostConnected[...]) — compare, record
// once on the edge, then replace the stored snapshot.
// It returns any windows that just transitioned to "started" with
// OnStart == force-cancel, so the caller can interrupt the in-flight jobs
// they now govern — see cancelForceCancelledJobs. This is deliberately a
// one-shot signal fired only on the edge: anything dispatched after the
// window opens is instead caught by the cancel-queued checkpoint in
// actionJob.Run before it ever reaches a Docker call.
func (a *app) checkMaintenanceTransitions(active []config.MaintenanceWindow, now time.Time) []config.MaintenanceWindow {
	current := make(map[string]string, len(active))
	byID := make(map[string]config.MaintenanceWindow, len(active))
	for _, w := range active {
		current[w.ID] = w.Title
		byID[w.ID] = w
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	prev := a.maintenanceActiveIDs
	var edges []maintenanceTransition
	var forceCancelStarted []config.MaintenanceWindow
	for id, title := range current {
		if _, wasActive := prev[id]; !wasActive {
			edges = append(edges, maintenanceTransition{WindowID: id, Title: title, Kind: "started", At: now})
			if w := byID[id]; w.OnStart == config.MaintenanceOnStartForceCancel {
				forceCancelStarted = append(forceCancelStarted, w)
			}
		}
	}
	for id, title := range prev {
		if _, stillActive := current[id]; !stillActive {
			edges = append(edges, maintenanceTransition{WindowID: id, Title: title, Kind: "ended", At: now})
		}
	}
	a.maintenanceActiveIDs = current

	if len(edges) > 0 {
		cutoff := now.Add(-maintenanceTransitionRetention)
		kept := a.maintenanceTransitions[:0]
		for _, t := range a.maintenanceTransitions {
			if t.At.After(cutoff) {
				kept = append(kept, t)
			}
		}
		a.maintenanceTransitions = append(kept, edges...)
	}

	return forceCancelStarted
}

// cancelForceCancelledJobs interrupts any in-flight action jobs that a
// force-cancel maintenance window now governs, by invoking their registered
// cancel funcs — executeAction threads the job's context into its own
// context.WithTimeout calls, so cancellation propagates straight into the
// in-flight Docker SDK call. Called once per newly-started force-cancel
// window (see checkMaintenanceTransitions's return value), not continuously.
func (a *app) cancelForceCancelledJobs(p maintenancePause) int {
	a.mu.Lock()
	var cancels []context.CancelFunc
	for _, reg := range a.activeJobCancels {
		if p.onStartFor(reg.jobID) == config.MaintenanceOnStartForceCancel {
			cancels = append(cancels, reg.cancel)
		}
	}
	a.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

// maintenanceAutoRemoveGrace is how long a finished one-off maintenance window
// is kept around (e.g. for review in the UI) before being auto-removed.
const maintenanceAutoRemoveGrace = 12 * time.Hour

// shouldAutoRemoveMaintenanceWindow reports whether a finished one-off window
// is old enough to be cleaned up automatically. Only strategies with a
// deterministic end are eligible — a Single window ends at Single.End, and a
// Manual window only has a deterministic end if the user gave it one via an
// optional Effective Date Range. Cron and the recurring strategies repeat
// indefinitely and are never auto-removed; the user deletes those manually.
func shouldAutoRemoveMaintenanceWindow(w config.MaintenanceWindow, now time.Time) bool {
	switch w.Strategy {
	case config.MaintenanceStrategySingle:
		return w.Single != nil && now.After(w.Single.End.Add(maintenanceAutoRemoveGrace))
	case config.MaintenanceStrategyManual:
		return w.EffectiveTo != nil && now.After(w.EffectiveTo.Add(maintenanceAutoRemoveGrace))
	default:
		return false
	}
}
