package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
)

// newOnStartTestApp builds a minimal app wired for actionJob.Run — with the
// given docker client and maintenance windows already loaded — covering the
// per-window OnStart behaviours (allow-finish / force-cancel).
func newOnStartTestApp(t *testing.T, docker dockerClient, windows []config.MaintenanceWindow) *app {
	t.Helper()
	store, err := history.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return &app{
		cfg:                  config.Config{Action: "restart", ActionTimeoutSeconds: 30},
		docker:               docker,
		history:              store,
		notifier:             notify.New(""),
		maintenanceWindows:   windows,
		lastNotified:         make(map[string]time.Time),
		cState:               make(map[string]*containerActionState),
		activeJobs:           make(map[string]bool),
		activeJobCancels:     make(map[string]activeJobCancel),
		lastJobScan:          make(map[string]time.Time),
		lastJobNotifications: make(map[string][]string),
	}
}

func TestActionJobCancelQueuedSkipsBeforeDockerCall(t *testing.T) {
	fake := &fakeDockerClient{}
	// allow-finish now always cancels queued (pre-Docker-call) actions; only
	// in-flight ones are let through. This test verifies the queued-skip path.
	a := newOnStartTestApp(t, fake, []config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-1"}, OnStart: config.MaintenanceOnStartAllowFinish},
	})

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "c1", Names: []string{"/demo"}},
		reason:    "unhealthy",
		action:    "restart",
		jobID:     "job-1",
		jobName:   "demo-job",
	}
	job.Run(context.Background())

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()
	if len(restarts) != 0 {
		t.Fatalf("expected the Docker call to be skipped entirely, got restarts=%v", restarts)
	}

	entries, err := a.history.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d: %#v", len(entries), entries)
	}
	if entries[0].Status != "skipped" || entries[0].Error != "maintenance window started" {
		t.Fatalf("unexpected history entry: %#v", entries[0])
	}
}

// TestAllQueuedJobsSkippedWhenWindowCoversMultipleJobs verifies that every job
// targeted by a maintenance window is independently skipped at the pre-Docker-call
// checkpoint — not just the first one.
func TestAllQueuedJobsSkippedWhenWindowCoversMultipleJobs(t *testing.T) {
	fake := &fakeDockerClient{}
	a := newOnStartTestApp(t, fake, []config.MaintenanceWindow{
		{
			ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual,
			JobIDs:  []string{"job-1", "job-2", "job-3"},
			OnStart: config.MaintenanceOnStartAllowFinish,
		},
	})

	for _, id := range []string{"job-1", "job-2", "job-3"} {
		job := actionJob{
			app:       a,
			container: containertypes.Summary{ID: id, Names: []string{"/" + id}},
			reason:    "unhealthy",
			action:    "restart",
			jobID:     id,
			jobName:   id,
		}
		job.Run(context.Background())
	}

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()
	if len(restarts) != 0 {
		t.Fatalf("expected all Docker calls skipped, got restarts=%v", restarts)
	}

	entries, err := a.history.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 history entries (one per job), got %d: %#v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Status != "skipped" || e.Error != "maintenance window started" {
			t.Errorf("unexpected entry: %#v", e)
		}
	}
}

// TestUntargetedJobNotSkippedByMaintenanceWindow verifies that a job not covered
// by any active maintenance window still runs normally — the guard must not
// apply false-positive pauses to unrelated jobs.
func TestUntargetedJobNotSkippedByMaintenanceWindow(t *testing.T) {
	fake := &fakeDockerClient{}
	// Window only covers job-1; job-2 should be unaffected.
	a := newOnStartTestApp(t, fake, []config.MaintenanceWindow{
		{
			ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual,
			JobIDs:  []string{"job-1"},
			OnStart: config.MaintenanceOnStartAllowFinish,
		},
	})

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "c2", Names: []string{"/other"}},
		reason:    "unhealthy",
		action:    "restart",
		jobID:     "job-2",
		jobName:   "other-job",
	}
	job.Run(context.Background())

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()
	if len(restarts) != 1 {
		t.Fatalf("expected untargeted job to proceed to Docker call, got restarts=%v", restarts)
	}
}

// TestHostTargetedWindowSkipsJobsOnThatHost verifies the jobsForHost path:
// when a maintenance window targets a Docker host rather than individual job IDs,
// any action job whose config is pinned to that host is still caught by the
// pre-Docker-call checkpoint.
func TestHostTargetedWindowSkipsJobsOnThatHost(t *testing.T) {
	fake := &fakeDockerClient{}
	a := newOnStartTestApp(t, fake, []config.MaintenanceWindow{
		{
			ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual,
			DockerHostIDs: []string{"remote-host"},
			OnStart:       config.MaintenanceOnStartAllowFinish,
		},
	})
	// Register a job pinned to remote-host so jobsForHost can resolve it.
	a.mu.Lock()
	a.jobs = []config.Job{{ID: "job-pinned", DockerHostIDs: []string{"remote-host"}}}
	a.mu.Unlock()

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "c1", Names: []string{"/pinned"}},
		reason:    "unhealthy",
		action:    "restart",
		jobID:     "job-pinned",
		jobName:   "pinned-job",
	}
	job.Run(context.Background())

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()
	if len(restarts) != 0 {
		t.Fatalf("expected job on paused host to be skipped, got restarts=%v", restarts)
	}
	entries, err := a.history.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Status != "skipped" {
		t.Fatalf("expected 1 skipped history entry, got %#v", entries)
	}
}

// TestLocalHostWindowSkipsJobWithNoJobID verifies the localOnStart path:
// when the "local" Docker host is in maintenance, actions that carry no jobID
// (global monitoring not associated with a specific job config) are also caught
// by the checkpoint via p.localOnStart rather than p.jobOnStart.
func TestLocalHostWindowSkipsJobWithNoJobID(t *testing.T) {
	fake := &fakeDockerClient{}
	a := newOnStartTestApp(t, fake, []config.MaintenanceWindow{
		{
			ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual,
			DockerHostIDs: []string{"local"},
			OnStart:       config.MaintenanceOnStartAllowFinish,
		},
	})

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "c1", Names: []string{"/global"}},
		reason:    "unhealthy",
		action:    "restart",
		jobID:     "", // no job config — uses localOnStart path
		jobName:   "",
	}
	job.Run(context.Background())

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()
	if len(restarts) != 0 {
		t.Fatalf("expected global action to be skipped under local host window, got restarts=%v", restarts)
	}
	entries, err := a.history.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Status != "skipped" {
		t.Fatalf("expected 1 skipped history entry, got %#v", entries)
	}
}

// slowDockerClient blocks RestartContainer until its context is cancelled, so
// a test can observe an in-flight Docker call and interrupt it deterministically
// — exercising the force-cancel mid-flight path rather than relying on timing.
type slowDockerClient struct {
	*fakeDockerClient
	started chan struct{}

	mu      sync.Mutex
	lastErr error
}

func (s *slowDockerClient) RestartContainer(ctx context.Context, _ string, _ int) error {
	close(s.started)
	<-ctx.Done()
	err := ctx.Err()
	s.mu.Lock()
	s.lastErr = err
	s.mu.Unlock()
	return err
}

func (s *slowDockerClient) lastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// releasableDockerClient blocks RestartContainer until its release channel is
// closed, then completes successfully — letting a test verify that an in-flight
// call was not interrupted (the allow-finish path).
type releasableDockerClient struct {
	*fakeDockerClient
	started chan struct{}
	release chan struct{}
}

func (r *releasableDockerClient) RestartContainer(ctx context.Context, id string, timeout int) error {
	close(r.started)
	select {
	case <-r.release:
		return r.fakeDockerClient.RestartContainer(ctx, id, timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestActionJobForceCancelInterruptsInFlightDockerCall(t *testing.T) {
	slow := &slowDockerClient{fakeDockerClient: &fakeDockerClient{}, started: make(chan struct{})}
	// No window active yet — the job must already be inside its Docker call
	// when one opens, to exercise mid-flight interruption rather than the
	// queued pre-check (which would bail before ever reaching here).
	a := newOnStartTestApp(t, slow, nil)

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "c1", Names: []string{"/demo"}},
		reason:    "unhealthy",
		action:    "restart",
		jobID:     "job-1",
		jobName:   "demo-job",
	}

	done := make(chan struct{})
	go func() {
		job.Run(context.Background())
		close(done)
	}()

	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Docker call never started")
	}

	// The window opens with force-cancel now that the call is in flight.
	a.mu.Lock()
	a.maintenanceWindows = []config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-1"}, OnStart: config.MaintenanceOnStartForceCancel},
	}
	a.mu.Unlock()

	pause := a.computeMaintenancePause(a.getJobs(), time.Now())
	if n := a.cancelForceCancelledJobs(pause); n != 1 {
		t.Fatalf("expected 1 in-flight job to be force-cancelled, got %d", n)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job.Run did not return after force-cancel")
	}

	if !errors.Is(slow.lastError(), context.Canceled) {
		t.Fatalf("expected RestartContainer's context to be cancelled, got %v", slow.lastError())
	}

	entries, err := a.history.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Status != "failed" {
		t.Fatalf("expected 1 failed history entry, got %#v", entries)
	}
	if !strings.Contains(entries[0].Error, "context canceled") {
		t.Fatalf("expected cancellation error in history, got %q", entries[0].Error)
	}
}

// TestActionJobForceCancelAlsoSkipsQueuedBeforeDockerCall verifies that
// force-cancel is a strict superset of allow-finish: a job dispatched after a
// force-cancel window opens is still skipped at the pre-Docker-call checkpoint,
// just like allow-finish — the only extra thing force-cancel adds is in-flight
// interruption (covered by TestActionJobForceCancelInterruptsInFlightDockerCall).
func TestActionJobForceCancelAlsoSkipsQueuedBeforeDockerCall(t *testing.T) {
	fake := &fakeDockerClient{}
	a := newOnStartTestApp(t, fake, []config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-1"}, OnStart: config.MaintenanceOnStartForceCancel},
	})

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "c1", Names: []string{"/demo"}},
		reason:    "unhealthy",
		action:    "restart",
		jobID:     "job-1",
		jobName:   "demo-job",
	}
	job.Run(context.Background())

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()
	if len(restarts) != 0 {
		t.Fatalf("expected Docker call skipped for force-cancel queued job, got restarts=%v", restarts)
	}

	entries, err := a.history.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d: %#v", len(entries), entries)
	}
	if entries[0].Status != "skipped" || entries[0].Error != "maintenance window started" {
		t.Fatalf("unexpected history entry: %#v", entries[0])
	}
}

// TestActionJobAllowFinishDoesNotCancelInFlightDockerCall verifies the "allow"
// half of allow-finish: a Docker call already in flight when an allow-finish
// window opens is left to complete normally — only force-cancel windows trigger
// context cancellation (via cancelForceCancelledJobs).
func TestActionJobAllowFinishDoesNotCancelInFlightDockerCall(t *testing.T) {
	rel := &releasableDockerClient{
		fakeDockerClient: &fakeDockerClient{},
		started:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	// No window active yet — the job must already be inside its Docker call
	// before the allow-finish window opens.
	a := newOnStartTestApp(t, rel, nil)

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "c1", Names: []string{"/demo"}},
		reason:    "unhealthy",
		action:    "restart",
		jobID:     "job-1",
		jobName:   "demo-job",
	}

	done := make(chan struct{})
	go func() {
		job.Run(context.Background())
		close(done)
	}()

	select {
	case <-rel.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Docker call never started")
	}

	// Open an allow-finish window now that the Docker call is in flight.
	a.mu.Lock()
	a.maintenanceWindows = []config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-1"}, OnStart: config.MaintenanceOnStartAllowFinish},
	}
	a.mu.Unlock()

	// allow-finish does NOT call cancelForceCancelledJobs — nothing to cancel.
	// Release the Docker call so it can finish.
	close(rel.release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job.Run did not return after Docker call release")
	}

	rel.fakeDockerClient.mu.Lock()
	restarts := append([]string(nil), rel.fakeDockerClient.restartIDs...)
	rel.fakeDockerClient.mu.Unlock()
	if len(restarts) != 1 {
		t.Fatalf("expected Docker call to complete for allow-finish in-flight job, got restarts=%v", restarts)
	}

	entries, err := a.history.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Status != "success" {
		t.Fatalf("expected 1 success history entry, got %#v", entries)
	}
}
