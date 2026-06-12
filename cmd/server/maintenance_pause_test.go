package main

import (
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
)

func appWithMaintenanceWindows(windows []config.MaintenanceWindow) *app {
	return &app{maintenanceWindows: windows}
}

func TestComputeMaintenancePauseDirectJob(t *testing.T) {
	now := time.Now()
	jobs := []config.Job{
		{ID: "job-1", Name: "a", Enabled: true},
		{ID: "job-2", Name: "b", Enabled: true},
	}
	a := appWithMaintenanceWindows([]config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-1"}},
	})

	pause := a.computeMaintenancePause(jobs, now)
	if !pause.jobPaused("job-1") {
		t.Error("expected job-1 to be paused")
	}
	if pause.jobPaused("job-2") {
		t.Error("expected job-2 to remain unpaused")
	}
	if pause.skipEntireScan {
		t.Error("did not expect skipEntireScan for a job-targeted window")
	}
}

func TestComputeMaintenancePauseHostExpandsToItsJobs(t *testing.T) {
	now := time.Now()
	jobs := []config.Job{
		{ID: "job-pinned", Name: "pinned", Enabled: true, DockerHostIDs: []string{"host-a"}},
		{ID: "job-other-host", Name: "other", Enabled: true, DockerHostIDs: []string{"host-b"}},
		{ID: "job-all-hosts", Name: "all", Enabled: true},
	}
	a := appWithMaintenanceWindows([]config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, DockerHostIDs: []string{"host-a"}},
	})

	pause := a.computeMaintenancePause(jobs, now)
	if !pause.hostPaused("host-a") {
		t.Error("expected host-a to be paused")
	}
	if pause.hostPaused("host-b") {
		t.Error("expected host-b to remain unpaused")
	}
	// jobsForHost: empty DockerHostIDs match every host, so the all-hosts job
	// is also pinned to host-a and must be paused along with it.
	if !pause.jobPaused("job-pinned") {
		t.Error("expected job-pinned (pinned to host-a) to be paused")
	}
	if !pause.jobPaused("job-all-hosts") {
		t.Error("expected job-all-hosts (matches every host) to be paused via host-a")
	}
	if pause.jobPaused("job-other-host") {
		t.Error("expected job-other-host (pinned to host-b) to remain unpaused")
	}
	if pause.skipEntireScan {
		t.Error("did not expect skipEntireScan for a non-local host")
	}
}

func TestComputeMaintenancePauseLocalHostSkipsEntireScan(t *testing.T) {
	now := time.Now()
	jobs := []config.Job{
		{ID: "job-local", Name: "local-job", Enabled: true, DockerHostIDs: []string{"local"}},
		{ID: "job-remote", Name: "remote-job", Enabled: true, DockerHostIDs: []string{"host-b"}},
	}
	a := appWithMaintenanceWindows([]config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, DockerHostIDs: []string{"local"}},
	})

	pause := a.computeMaintenancePause(jobs, now)
	if !pause.skipEntireScan {
		t.Error("expected skipEntireScan=true when the local host is under maintenance")
	}
	if !pause.hostPaused("local") {
		t.Error("expected local host to be marked paused")
	}
	if !pause.jobPaused("job-local") {
		t.Error("expected job-local to be paused via local host expansion")
	}
}

func TestComputeMaintenancePauseMixedJobsAndHosts(t *testing.T) {
	now := time.Now()
	jobs := []config.Job{
		{ID: "job-direct", Name: "direct", Enabled: true},
		{ID: "job-via-host", Name: "via-host", Enabled: true, DockerHostIDs: []string{"host-a"}},
		{ID: "job-untouched", Name: "untouched", Enabled: true, DockerHostIDs: []string{"host-b"}},
	}
	a := appWithMaintenanceWindows([]config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-direct"}, DockerHostIDs: []string{"host-a"}},
	})

	pause := a.computeMaintenancePause(jobs, now)
	if !pause.jobPaused("job-direct") || !pause.jobPaused("job-via-host") {
		t.Errorf("expected both directly-targeted and host-expanded jobs paused, got %#v", pause.pausedJobIDs)
	}
	if pause.jobPaused("job-untouched") {
		t.Error("expected job pinned to an unaffected host to remain unpaused")
	}
}

func TestComputeMaintenancePauseIgnoresInactiveWindows(t *testing.T) {
	now := time.Now()
	jobs := []config.Job{{ID: "job-1", Name: "a", Enabled: true}}
	a := appWithMaintenanceWindows([]config.MaintenanceWindow{
		{ID: "m1", Active: false, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-1"}},
	})

	pause := a.computeMaintenancePause(jobs, now)
	if pause.jobPaused("job-1") {
		t.Error("expected inactive window to have no pausing effect")
	}
	if len(pause.active) != 0 {
		t.Errorf("expected zero active windows, got %d", len(pause.active))
	}
}

func TestFilterPausedJobs(t *testing.T) {
	jobs := []config.Job{
		{ID: "job-1", Name: "a"},
		{ID: "job-2", Name: "b"},
		{ID: "job-3", Name: "c"},
	}
	pause := maintenancePause{pausedJobIDs: map[string]bool{"job-2": true}}

	out := filterPausedJobs(jobs, pause)
	if len(out) != 2 {
		t.Fatalf("expected 2 jobs after filtering, got %d: %#v", len(out), out)
	}
	for _, j := range out {
		if j.ID == "job-2" {
			t.Errorf("expected job-2 to be filtered out, found in result: %#v", out)
		}
	}
}

// ── integration: a paused job must produce zero scan activity ────────────────

func TestScanOnceSkipsJobUnderActiveMaintenanceWindow(t *testing.T) {
	pausedContainer := containertypes.Summary{ID: "c1", Names: []string{"paused-app"}, State: "running", Status: "Up 2 hours (unhealthy)"}
	activeContainer := containertypes.Summary{ID: "c2", Names: []string{"active-app"}, State: "running", Status: "Up 2 hours (unhealthy)"}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{pausedContainer, activeContainer}}

	jobs := []config.Job{
		{ID: "job-paused", Name: "paused-job", Action: "restart", Enabled: true, ContainerNameFilter: "paused-app"},
		{ID: "job-active", Name: "active-job", Action: "restart", Enabled: true, ContainerNameFilter: "active-app"},
	}
	a, pool := newScanOnceApp(t, fake, jobs)
	a.maintenanceWindows = []config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-paused"}},
	}

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 1 || restarts[0] != "c2" {
		t.Fatalf("expected only the unpaused container's job to fire (c2), got %v", restarts)
	}
}

func TestScanOnceSkipsEntirelyWhenLocalHostUnderMaintenance(t *testing.T) {
	c := containertypes.Summary{ID: "c1", Names: []string{"app-one"}, State: "running", Status: "Up 2 hours (unhealthy)"}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	catchAll := []config.Job{{ID: "r1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newScanOnceApp(t, fake, catchAll)
	a.maintenanceWindows = []config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, DockerHostIDs: []string{"local"}},
	}

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 0 {
		t.Fatalf("expected no scan activity while local host is under maintenance, got restarts=%v", restarts)
	}
}
