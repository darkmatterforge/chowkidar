package main

import (
	"context"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
	"chowkidar/internal/worker"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newRecoveryApp(t *testing.T, fake *fakeDockerClient, jobs []config.Job) (*app, *worker.Pool) {
	t.Helper()
	dir := t.TempDir()
	store, err := history.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	pool := worker.NewPool(2, 8)
	a := &app{
		cfg: config.Config{
			Action:               "restart",
			ActionTimeoutSeconds: 5,
		},
		docker:               fake,
		pool:                 pool,
		history:              store,
		notifier:             notify.New(""),
		jobs:                 jobs,
		lastNotified:         make(map[string]time.Time),
		cState:               make(map[string]*containerActionState),
		activeJobs:           make(map[string]bool),
		lastJobScan:          make(map[string]time.Time),
		lastJobNotifications: make(map[string][]string),
	}
	return a, pool
}

func unhealthyContainer(id, name string) containertypes.Summary {
	return containertypes.Summary{
		ID:     id,
		Names:  []string{"/" + name},
		State:  "running",
		Status: "Up 2 hours (unhealthy)",
	}
}

// ── unhealthy → restart ───────────────────────────────────────────────────────

func TestHealthRecovery_UnhealthyContainerQueuesRestart(t *testing.T) {
	c := unhealthyContainer("aaa", "web-app")
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	catchAll := []config.Job{{ID: "j1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newRecoveryApp(t, fake, catchAll)

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 1 || restarts[0] != "aaa" {
		t.Fatalf("expected restart for aaa, got %v", restarts)
	}
}

func TestHealthRecovery_SuccessfulRestartWritesHistoryEntry(t *testing.T) {
	fake := &fakeDockerClient{}
	a := &app{
		cfg:                config.Config{ActionTimeoutSeconds: 5},
		docker:             fake,
		notifier:           notify.New(""),
		lastNotified:         make(map[string]time.Time),
		cState:               make(map[string]*containerActionState),
		activeJobs:           make(map[string]bool),
		lastJobNotifications: make(map[string][]string),
	}
	dir := t.TempDir()
	store, err := history.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	a.history = store

	job := actionJob{
		app:       a,
		container: unhealthyContainer("bbb", "api"),
		reason:    "unhealthy",
		action:    "restart",
	}
	job.Run(context.Background())

	entries, err := store.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one history entry after successful restart")
	}
	var found bool
	for _, e := range entries {
		if e.Status == "success" && e.ContainerName == "api" {
			found = true
			break
		}
	}
	if !found {
		statuses := make([]string, len(entries))
		for i, e := range entries {
			statuses[i] = e.Status
		}
		t.Fatalf("expected a 'success' entry for container 'api', got statuses: %v", statuses)
	}
}

// ── recovery detection ────────────────────────────────────────────────────────

func TestHealthRecovery_RecoveredContainerClearsActionCycle(t *testing.T) {
	c := unhealthyContainer("ccc", "db")
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	catchAll := []config.Job{{ID: "j1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newRecoveryApp(t, fake, catchAll)

	// First scan: container unhealthy → restart queued
	a.scanOnce()
	pool.Stop()

	// Verify an action cycle was opened for this container
	a.mu.RLock()
	var cycle int
	if s := a.cState["db"]; s != nil {
		cycle = s.cycle
	}
	a.mu.RUnlock()
	if cycle == 0 {
		t.Fatal("expected cState.cycle to be incremented after restart")
	}

	// Container recovers — remove from unhealthy list
	fake.mu.Lock()
	fake.unhealthy = nil
	fake.mu.Unlock()

	// Second scan: container healthy → cycle cleared, recovery event logged
	pool2 := worker.NewPool(2, 8)
	a.pool = pool2
	a.scanOnce()
	pool2.Stop()

	a.mu.RLock()
	var cycleAfter int
	if s := a.cState["db"]; s != nil {
		cycleAfter = s.cycle
	}
	a.mu.RUnlock()
	if cycleAfter != 0 {
		t.Fatalf("expected cState cleared after recovery, got cycle=%d", cycleAfter)
	}
}

func TestHealthRecovery_RecoveredContainerWritesRecoveryHistoryEntry(t *testing.T) {
	c := unhealthyContainer("ddd", "cache")
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	catchAll := []config.Job{{ID: "j1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newRecoveryApp(t, fake, catchAll)

	a.scanOnce()
	pool.Stop()

	// Container recovers
	fake.mu.Lock()
	fake.unhealthy = nil
	fake.mu.Unlock()

	pool2 := worker.NewPool(2, 8)
	a.pool = pool2
	a.scanOnce()
	pool2.Stop()

	entries, err := a.history.List(20)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var recovered bool
	for _, e := range entries {
		if e.Status == "recovered" && e.ContainerName == "cache" {
			recovered = true
			break
		}
	}
	if !recovered {
		statuses := make([]string, len(entries))
		for i, e := range entries {
			statuses[i] = e.Status
		}
		t.Fatalf("expected a 'recovered' history entry for 'cache', got statuses: %v", statuses)
	}
}

// ── job filter matching ───────────────────────────────────────────────────────

func TestHealthRecovery_JobNameFilterCSVMatches(t *testing.T) {
	c1 := unhealthyContainer("e1", "nginx")
	c2 := unhealthyContainer("e2", "redis")
	c3 := unhealthyContainer("e3", "unrelated-svc")
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c1, c2, c3}}
	// Job only covers nginx and redis via CSV
	job := config.Job{ID: "j1", Name: "web", Action: "restart", Enabled: true, ContainerNameFilter: "nginx,redis"}
	a, pool := newRecoveryApp(t, fake, []config.Job{job})

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 2 {
		t.Fatalf("expected 2 restarts (nginx + redis), got %v", restarts)
	}
	for _, id := range restarts {
		if id != "e1" && id != "e2" {
			t.Fatalf("unexpected restart id %s — only nginx/redis should match", id)
		}
	}
}

func TestHealthRecovery_JobNameFilterNoMatchSkipsContainer(t *testing.T) {
	c := unhealthyContainer("f1", "other-app")
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	job := config.Job{ID: "j1", Name: "web", Action: "restart", Enabled: true, ContainerNameFilter: "nginx"}
	a, pool := newRecoveryApp(t, fake, []config.Job{job})

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 0 {
		t.Fatalf("expected no restarts (name filter mismatch), got %v", restarts)
	}
}

func TestHealthRecovery_JobLabelFilterMatches(t *testing.T) {
	c := unhealthyContainer("g1", "frontend")
	c.Labels = map[string]string{"app": "web", "env": "prod"}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	job := config.Job{ID: "j1", Name: "web", Action: "restart", Enabled: true, ContainerLabelFilter: "env=prod"}
	a, pool := newRecoveryApp(t, fake, []config.Job{job})

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 1 || restarts[0] != "g1" {
		t.Fatalf("expected restart for g1 (label match), got %v", restarts)
	}
}

func TestHealthRecovery_JobLabelFilterNoMatchSkipsContainer(t *testing.T) {
	c := unhealthyContainer("h1", "backend")
	c.Labels = map[string]string{"app": "api", "env": "staging"}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	job := config.Job{ID: "j1", Name: "web", Action: "restart", Enabled: true, ContainerLabelFilter: "env=prod"}
	a, pool := newRecoveryApp(t, fake, []config.Job{job})

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 0 {
		t.Fatalf("expected no restart (label mismatch), got %v", restarts)
	}
}

func TestHealthRecovery_DisabledJobDoesNotRestartContainer(t *testing.T) {
	c := unhealthyContainer("i1", "worker")
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	job := config.Job{ID: "j1", Name: "disabled", Action: "restart", Enabled: false}
	a, pool := newRecoveryApp(t, fake, []config.Job{job})

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 0 {
		t.Fatalf("expected no restart for disabled job, got %v", restarts)
	}
}

func TestHealthRecovery_ActionNoneDoesNotCallDocker(t *testing.T) {
	c := unhealthyContainer("j1", "monitor")
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c}}
	job := config.Job{ID: "j1", Name: "observe-only", Action: "none", Enabled: true}
	a, pool := newRecoveryApp(t, fake, []config.Job{job})

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	starts := append([]string(nil), fake.startIDs...)
	stops := append([]string(nil), fake.stopIDs...)
	fake.mu.Unlock()

	if len(restarts)+len(starts)+len(stops) != 0 {
		t.Fatalf("action=none must not call any docker operation, got restarts=%v starts=%v stops=%v", restarts, starts, stops)
	}
}

func TestHealthRecovery_MultipleContainersAllRestarted(t *testing.T) {
	containers := []containertypes.Summary{
		unhealthyContainer("k1", "svc-a"),
		unhealthyContainer("k2", "svc-b"),
		unhealthyContainer("k3", "svc-c"),
	}
	fake := &fakeDockerClient{unhealthy: containers}
	catchAll := []config.Job{{ID: "j1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newRecoveryApp(t, fake, catchAll)

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 3 {
		t.Fatalf("expected all 3 containers restarted, got %v", restarts)
	}
}
