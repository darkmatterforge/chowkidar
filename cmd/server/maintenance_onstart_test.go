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
// per-window OnStart behaviours (allow-finish / cancel-queued / force-cancel).
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
	a := newOnStartTestApp(t, fake, []config.MaintenanceWindow{
		{ID: "m1", Active: true, Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-1"}, OnStart: config.MaintenanceOnStartCancelQueued},
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

func TestActionJobForceCancelInterruptsInFlightDockerCall(t *testing.T) {
	slow := &slowDockerClient{fakeDockerClient: &fakeDockerClient{}, started: make(chan struct{})}
	// No window active yet — the job must already be inside its Docker call
	// when one opens, to exercise mid-flight interruption rather than the
	// cancel-queued pre-check (which would bail before ever reaching here).
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
