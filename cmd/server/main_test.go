package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
	"chowkidar/internal/dockerhealth"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
	"chowkidar/internal/worker"
)

type fakeDockerClient struct {
	mu          sync.Mutex
	restartErrs []error
	startErr    error
	stopErr     error
	inspect     containertypes.InspectResponse
	unhealthy   []containertypes.Summary
	exited      []containertypes.Summary
	restarting  []containertypes.Summary
	starting    []containertypes.Summary
	all         []containertypes.Summary
	restartIDs  []string
	startIDs    []string
	stopIDs     []string
	exitedCalls int
}

func (f *fakeDockerClient) UnhealthyContainers(_ context.Context) ([]containertypes.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]containertypes.Summary, len(f.unhealthy))
	copy(out, f.unhealthy)
	return out, nil
}
func (f *fakeDockerClient) ExitedContainers(_ context.Context) ([]containertypes.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exitedCalls++
	out := make([]containertypes.Summary, len(f.exited))
	copy(out, f.exited)
	return out, nil
}
func (f *fakeDockerClient) RestartingContainers(_ context.Context) ([]containertypes.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]containertypes.Summary, len(f.restarting))
	copy(out, f.restarting)
	return out, nil
}
func (f *fakeDockerClient) StartingContainers(_ context.Context) ([]containertypes.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]containertypes.Summary, len(f.starting))
	copy(out, f.starting)
	return out, nil
}
func (f *fakeDockerClient) AllContainers(_ context.Context) ([]containertypes.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]containertypes.Summary, len(f.all))
	copy(out, f.all)
	return out, nil
}
func (f *fakeDockerClient) RestartContainer(_ context.Context, id string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartIDs = append(f.restartIDs, id)
	if len(f.restartErrs) == 0 {
		return nil
	}
	err := f.restartErrs[0]
	f.restartErrs = f.restartErrs[1:]
	return err
}
func (f *fakeDockerClient) StartContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startIDs = append(f.startIDs, id)
	return f.startErr
}
func (f *fakeDockerClient) StopContainer(_ context.Context, id string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopIDs = append(f.stopIDs, id)
	return f.stopErr
}
func (f *fakeDockerClient) InspectContainer(_ context.Context, _ string) (containertypes.InspectResponse, error) {
	return f.inspect, nil
}
func (*fakeDockerClient) Ping(_ context.Context) error { return nil }

type fakeNotifier struct {
	titles []string
	bodies []string
}

func (f *fakeNotifier) Send(title, body string) error {
	f.titles = append(f.titles, title)
	f.bodies = append(f.bodies, body)
	return nil
}

func TestExecuteActionRestartStartStopNone(t *testing.T) {
	fake := &fakeDockerClient{}
	app := &app{cfg: config.Config{ActionTimeoutSeconds: 5}, docker: fake, httpClient: &http.Client{Timeout: 5 * time.Second}, notifier: notify.New("")}
	container := containertypes.Summary{ID: "abc", Names: []string{"demo"}}
	for _, action := range []string{"restart", "start", "stop", "none"} {
		if _, err := app.executeAction(context.Background(), app.docker, action, "", container, 0, 0); err != nil {
			t.Fatalf("executeAction(%s) error = %v", action, err)
		}
	}
	if len(fake.restartIDs) != 1 || len(fake.startIDs) != 1 || len(fake.stopIDs) != 1 {
		t.Fatalf("unexpected docker calls: %#v", fake)
	}
}

func TestExecuteActionRunScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "heal.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write script error = %v", err)
	}
	app := &app{
		cfg: config.Config{
			ActionTimeoutSeconds: 5,
			RunScriptPath:        scriptPath,
			StartExited:          true,
		},
		docker:   &fakeDockerClient{},
		notifier: notify.New(""),
		scripts:  []config.ScriptEntry{{Path: scriptPath, Enabled: true}},
	}
	container := containertypes.Summary{ID: "abc", Names: []string{"demo"}}
	if _, err := app.executeAction(context.Background(), app.docker, "run-script", "", container, 0, 0); err != nil {
		t.Fatalf("run-script error = %v", err)
	}
}

func TestExecuteActionInvalidPathsUnsupportedAction(t *testing.T) {
	app := &app{cfg: config.Config{ActionTimeoutSeconds: 5}, docker: &fakeDockerClient{}, httpClient: &http.Client{Timeout: 5 * time.Second}, notifier: notify.New("")}
	container := containertypes.Summary{ID: "abc", Names: []string{"demo"}}
	if _, err := app.executeAction(context.Background(), app.docker, "run-script", "", container, 0, 0); err == nil {
		t.Fatal("expected run-script error with empty path")
	}
	if _, err := app.executeAction(context.Background(), app.docker, "bogus", "", container, 0, 0); err == nil {
		t.Fatal("expected unsupported action error")
	}
	if _, err := app.executeAction(context.Background(), app.docker, "run-script", "#!/bin/sh\nexit 0\n", container, 0, 0); err != nil {
		t.Fatalf("expected inline script to succeed: %v", err)
	}
	if _, err := app.executeAction(context.Background(), app.docker, "run-script", "#!/bin/sh\nexit 1\n", container, 0, 0); err == nil {
		t.Fatal("expected inline script with exit 1 to fail")
	}
}

func TestRetryHistoryLogging(t *testing.T) {
	dir := t.TempDir()
	historyStore, err := history.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// In the new one-action-per-loop model each Run() call is one cycle.
	// postActionWaitSeconds=0 so no deadline is set between cycles.
	fake := &fakeDockerClient{restartErrs: []error{errors.New("first"), errors.New("second"), nil}}
	a := &app{
		cfg:                config.Config{RetryCount: 3, ActionTimeoutSeconds: 5},
		docker:             fake,
		history:            historyStore,
		notifier:           notify.New(""),
		lastNotified:       make(map[string]time.Time),
		actionCycle:        make(map[string]int),
		retriesExhausted:   make(map[string]bool),
		activeJobs:         make(map[string]bool),
		postActionDeadline: make(map[string]time.Time),
	}
	job := actionJob{app: a, container: containertypes.Summary{ID: "c1", Names: []string{"demo"}}, reason: "unhealthy", action: "restart"}
	job.Run(context.Background()) // cycle 1: fail
	job.Run(context.Background()) // cycle 2: fail
	job.Run(context.Background()) // cycle 3: success

	entries, err := historyStore.List(10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	// cycle 3 succeeds AND hits the retry limit — logs success + exhausted: 4 total
	if len(entries) != 4 {
		t.Fatalf("expected 4 history entries, got %d", len(entries))
	}
	if entries[0].Status != "exhausted" {
		t.Fatalf("latest entry not exhausted: %#v", entries[0])
	}
	if entries[1].Attempt != 3 || entries[1].Status != "success" {
		t.Fatalf("success entry not at index 1: %#v", entries[1])
	}
	if entries[2].Attempt != 2 || entries[3].Attempt != 1 {
		t.Fatalf("attempt ordering incorrect: %#v", entries)
	}
}

func TestRetriesExhaustedAfterMaxCycles(t *testing.T) {
	dir := t.TempDir()
	historyStore, _ := history.NewStore(dir)
	fake := &fakeDockerClient{restartErrs: []error{errors.New("fail"), errors.New("fail"), errors.New("fail"), errors.New("fail")}}
	a := &app{
		cfg:                config.Config{RetryCount: 3, ActionTimeoutSeconds: 5},
		docker:             fake,
		history:            historyStore,
		notifier:           notify.New(""),
		lastNotified:       make(map[string]time.Time),
		actionCycle:        make(map[string]int),
		retriesExhausted:   make(map[string]bool),
		activeJobs:         make(map[string]bool),
		postActionDeadline: make(map[string]time.Time),
	}
	job := actionJob{app: a, container: containertypes.Summary{ID: "c1", Names: []string{"demo"}}, reason: "unhealthy", action: "restart"}

	if a.isRetriesExhausted("demo") {
		t.Fatal("should not be exhausted before any runs")
	}
	job.Run(context.Background()) // cycle 1: fail
	job.Run(context.Background()) // cycle 2: fail
	job.Run(context.Background()) // cycle 3: fail → exhausted
	if !a.isRetriesExhausted("demo") {
		t.Fatal("expected exhausted after 3 failed cycles")
	}

	// Further Run() calls must be skipped — no additional docker calls
	beforeCount := len(fake.restartIDs)
	job.Run(context.Background())
	if len(fake.restartIDs) != beforeCount {
		t.Fatal("expected no action after exhaustion")
	}
}

func TestShutdownApplicationStopsServer(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Start()
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("pre-shutdown GET failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	stopMonitor := make(chan struct{})
	shutdownApplication(stopMonitor, server.Config, 2*time.Second)

	select {
	case <-stopMonitor:
	case <-time.After(2 * time.Second):
		t.Fatal("stop monitor channel was not closed")
	}

	respAfter, err := http.Get(server.URL)
	if err == nil {
		_, _ = io.Copy(io.Discard, respAfter.Body)
		_ = respAfter.Body.Close()
		t.Fatal("expected GET to fail after shutdown")
	}
}

func TestScanOnceSafetyGateSkipsExitedWithoutFilters(t *testing.T) {
	fake := &fakeDockerClient{exited: []containertypes.Summary{{ID: "e1", Names: []string{"demo-exited"}}}}
	pool := worker.NewPool(1, 2)
	defer pool.Stop()

	a := &app{
		cfg: config.Config{
			RetryCount:                    1,
			ActionTimeoutSeconds:          5,
			StartExited:                   true,
			RequireFilterForExited:        true,
			DockerClientRetryCount:        1,
			DockerClientRetryDelaySeconds: 1,
		},
		docker:   fake,
		pool:     pool,
		notifier: notify.New(""),
	}

	a.scanOnce()

	if fake.exitedCalls != 0 {
		t.Fatalf("expected exited scan to be skipped by safety gate, calls=%d", fake.exitedCalls)
	}
}

func TestShouldStartExitedContainerHonorsJobStartExited(t *testing.T) {
	a := &app{docker: &fakeDockerClient{}}
	container := containertypes.Summary{ID: "e1", Names: []string{"match-me"}}
	cfg := config.Config{}

	matchJobs := []config.Job{{
		ID:                  "r1",
		Name:                "start job",
		Enabled:             true,
		StartExited:         true,
		ContainerNameFilter: "match-me",
	}}
	if !a.shouldStartExitedContainer(context.Background(), a.docker, container, cfg, matchJobs) {
		t.Fatal("expected job with startExited=true to allow exited start")
	}

	noStartJobs := []config.Job{{
		ID:                  "r2",
		Name:                "no start job",
		Enabled:             true,
		StartExited:         false,
		ContainerNameFilter: "match-me",
	}}
	if a.shouldStartExitedContainer(context.Background(), a.docker, container, cfg, noStartJobs) {
		t.Fatal("expected job with startExited=false to block exited start")
	}
}

func TestSendNotificationIncludesExternalHostname(t *testing.T) {
	n := &fakeNotifier{}
	a := &app{
		cfg:      config.Config{ExternalHostname: "pool.example"},
		notifier: n,
	}

	if err := a.sendNotification("startup", "Application started"); err != nil {
		t.Fatalf("sendNotification() error = %v", err)
	}
	if len(n.titles) != 1 || len(n.bodies) != 1 {
		t.Fatalf("expected one notification, got titles=%d bodies=%d", len(n.titles), len(n.bodies))
	}
	if !strings.Contains(n.titles[0], "[chowkidar@pool.example] startup") {
		t.Fatalf("unexpected title: %q", n.titles[0])
	}
	if !strings.Contains(n.bodies[0], "host=pool.example") {
		t.Fatalf("expected hostname marker in body, got %q", n.bodies[0])
	}
}

func TestSendStartupNotificationIfReady(t *testing.T) {
	n := &fakeNotifier{}
	a := &app{cfg: config.Config{BootNotification: true}, notifier: n}

	// skipped when BootNotification is false
	a.cfg.BootNotification = false
	a.diagnostics = dockerhealth.Diagnostics{DockerReachable: true, Details: "ok"}
	if err := a.sendStartupNotificationIfReady(); err != nil {
		t.Fatalf("sendStartupNotificationIfReady() error = %v", err)
	}
	if len(n.titles) != 0 {
		t.Fatalf("expected no notification when BootNotification=false, got %d", len(n.titles))
	}

	a.cfg.BootNotification = true

	a.diagnostics = dockerhealth.Diagnostics{DockerReachable: false, Details: "cannot connect"}
	if err := a.sendStartupNotificationIfReady(); err != nil {
		t.Fatalf("sendStartupNotificationIfReady() error = %v", err)
	}
	if len(n.titles) != 0 {
		t.Fatalf("expected startup notification to be skipped when diagnostics fail, got %d", len(n.titles))
	}

	a.diagnostics = dockerhealth.Diagnostics{DockerReachable: true, Details: "ok"}
	if err := a.sendStartupNotificationIfReady(); err != nil {
		t.Fatalf("sendStartupNotificationIfReady() error = %v", err)
	}
	if len(n.titles) != 1 {
		t.Fatalf("expected one startup notification after diagnostics pass, got %d", len(n.titles))
	}
}

func TestDashboardHandlersExposeHealthContainersAndHistory(t *testing.T) {
	dir := t.TempDir()
	historyStore, err := history.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := historyStore.Append(history.Entry{
		Timestamp:     time.Now().UTC(),
		ContainerID:   "c1",
		ContainerName: "demo",
		Reason:        "unhealthy",
		Action:        "restart",
		Attempt:       1,
		Status:        "success",
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	a := &app{
		cfg:       config.Config{Action: "restart"},
		notifier:  notify.New(""),
		history:   historyStore,
		pool:      worker.NewPool(1, 2),
		unhealthy: []containertypes.Summary{{ID: "u1", Names: []string{"demo"}, State: "running", Status: "Up (unhealthy)"}},
		exited:    []containertypes.Summary{{ID: "e1", Names: []string{"demo-exited"}, State: "exited", Status: "Exited (1)"}},
		lastScan:  time.Now().UTC(),
	}
	defer a.stopPool()

	healthReq := httptest.NewRequest(http.MethodGet, "/api/health", http.NoBody)
	healthRR := httptest.NewRecorder()
	a.handleHealth(healthRR, healthReq)
	if healthRR.Code != http.StatusOK {
		t.Fatalf("health status = %d", healthRR.Code)
	}
	var health map[string]any
	if err := json.Unmarshal(healthRR.Body.Bytes(), &health); err != nil {
		t.Fatalf("health decode error = %v", err)
	}
	if health["action"] != "restart" {
		t.Fatalf("unexpected health action: %#v", health)
	}

	containersReq := httptest.NewRequest(http.MethodGet, "/api/containers", http.NoBody)
	containersRR := httptest.NewRecorder()
	a.handleContainers(containersRR, containersReq)
	if containersRR.Code != http.StatusOK {
		t.Fatalf("containers status = %d", containersRR.Code)
	}
	var containers struct {
		Unhealthy []map[string]any `json:"unhealthy"`
		Exited    []map[string]any `json:"exited"`
		All       []map[string]any `json:"all"`
	}
	if err := json.Unmarshal(containersRR.Body.Bytes(), &containers); err != nil {
		t.Fatalf("containers decode error = %v", err)
	}
	if len(containers.Unhealthy) != 1 || len(containers.Exited) != 1 {
		t.Fatalf("unexpected containers payload: %#v", containers)
	}
	if len(containers.All) != 2 {
		t.Fatalf("expected all containers payload fallback, got %#v", containers)
	}

	historyReq := httptest.NewRequest(http.MethodGet, "/api/history?limit=1", http.NoBody)
	historyRR := httptest.NewRecorder()
	a.handleHistory(historyRR, historyReq)
	if historyRR.Code != http.StatusOK {
		t.Fatalf("history status = %d", historyRR.Code)
	}
	var out struct {
		Entries []history.Entry `json:"entries"`
	}
	if err := json.Unmarshal(historyRR.Body.Bytes(), &out); err != nil {
		t.Fatalf("history decode error = %v", err)
	}
	if len(out.Entries) != 1 || out.Entries[0].ContainerName != "demo" {
		t.Fatalf("unexpected history payload: %#v", out)
	}
}

func TestJobsAndNotificationsHandlersPersistState(t *testing.T) {
	dir := t.TempDir()
	a := &app{
		cfg:      config.Config{ConfigDir: dir, Action: "restart"},
		notifier: notify.New(""),
		pool:     worker.NewPool(1, 2),
	}
	defer a.stopPool()

	jobBody := []byte(`{"name":"job-one","action":"restart","containerNameFilter":"demo","enabled":true,"startExited":true}`)
	jobReq := httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewReader(jobBody))
	jobReq.Header.Set("Content-Type", "application/json")
	jobRR := httptest.NewRecorder()
	a.handleJobs(jobRR, jobReq)
	if jobRR.Code != http.StatusCreated {
		t.Fatalf("job create status = %d body=%s", jobRR.Code, jobRR.Body.String())
	}

	getJobsReq := httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody)
	getJobsRR := httptest.NewRecorder()
	a.handleJobs(getJobsRR, getJobsReq)
	if getJobsRR.Code != http.StatusOK {
		t.Fatalf("job list status = %d", getJobsRR.Code)
	}
	var jobsOut struct {
		Jobs []config.Job `json:"jobs"`
	}
	if err := json.Unmarshal(getJobsRR.Body.Bytes(), &jobsOut); err != nil {
		t.Fatalf("job list decode error = %v", err)
	}
	if len(jobsOut.Jobs) != 1 || !jobsOut.Jobs[0].StartExited {
		t.Fatalf("unexpected jobs payload: %#v", jobsOut)
	}

	notifyBody := []byte(`{"profiles":[{"id":"p1","name":"Discord","provider":"discord","details":{"token":"token","webhookid":"id"},"service":"discord://token@id","enabled":true},{"id":"p2","name":"Slack","provider":"slack","details":{"tokena":"ta","tokenb":"tb","tokenc":"tc"},"service":"slack://ta/tb/tc","enabled":false}]}`)
	notifyReq := httptest.NewRequest(http.MethodPut, "/api/notifications", bytes.NewReader(notifyBody))
	notifyReq.Header.Set("Content-Type", "application/json")
	notifyRR := httptest.NewRecorder()
	a.handleNotifications(notifyRR, notifyReq)
	if notifyRR.Code != http.StatusOK {
		t.Fatalf("notification save status = %d body=%s", notifyRR.Code, notifyRR.Body.String())
	}

	if a.getConfig().AppriseServices != "discord://token@id" {
		t.Fatalf("expected enabled service only, got %q", a.getConfig().AppriseServices)
	}
	if got := a.getNotificationProfiles(); len(got) != 2 {
		t.Fatalf("expected 2 notification profiles, got %d", len(got))
	}

	hostsBody := []byte(`{"profiles":[{"id":"h1","name":"Local","type":"socket","endpoint":"/var/run/docker.sock","enabled":true}],"activeHostID":"h1"}`)
	hostsReq := httptest.NewRequest(http.MethodPut, "/api/docker-hosts", bytes.NewReader(hostsBody))
	hostsReq.Header.Set("Content-Type", "application/json")
	hostsRR := httptest.NewRecorder()
	a.handleDockerHosts(hostsRR, hostsReq)
	if hostsRR.Code != http.StatusOK {
		t.Fatalf("docker hosts save status = %d body=%s", hostsRR.Code, hostsRR.Body.String())
	}
	if got := a.getDockerHostProfiles(); len(got) != 1 || got[0].Endpoint != "/var/run/docker.sock" {
		t.Fatalf("unexpected docker host profiles: %#v", got)
	}
}

func TestJobsHandlerSupportsSearchAndGroupFiltering(t *testing.T) {
	a := &app{
		jobs: []config.Job{
			{ID: "1", Name: "db restart", Group: "database", Action: "restart", Enabled: true, ContainerNameFilter: "db"},
			{ID: "2", Name: "api stop", Group: "frontend", Action: "stop", Enabled: false, ContainerNameFilter: "api"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs?q=db&enabled=true&group=database", http.NoBody)
	rr := httptest.NewRecorder()
	a.handleJobs(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("jobs filtered status = %d", rr.Code)
	}
	var out struct {
		Jobs []config.Job `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode filtered jobs error = %v", err)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].ID != "1" {
		t.Fatalf("unexpected filtered jobs payload: %#v", out)
	}
}

func newScanOnceApp(t *testing.T, fake *fakeDockerClient, jobs []config.Job) (*app, *worker.Pool) {
	t.Helper()
	pool := worker.NewPool(2, 8)
	a := &app{
		cfg: config.Config{
			Action:                        "restart",
			ActionTimeoutSeconds:          5,
			RetryCount:                    1,
			DockerClientRetryCount:        1,
			DockerClientRetryDelaySeconds: 1,
		},
		docker:               fake,
		pool:                 pool,
		notifier:             notify.New(""),
		jobs:                 jobs,
		lastNotified:         make(map[string]time.Time),
		actionCycle:          make(map[string]int),
		retriesExhausted:     make(map[string]bool),
		postActionDeadline:   make(map[string]time.Time),
		activeJobs:           make(map[string]bool),
		lastJobScan:          make(map[string]time.Time),
		lastJobNotifications: make(map[string][]string),
	}
	return a, pool
}

func TestScanOnceSkipsCooldownContainerContinuesOthers(t *testing.T) {
	c1 := containertypes.Summary{ID: "c1", Names: []string{"app-one"}}
	c2 := containertypes.Summary{ID: "c2", Names: []string{"app-two"}}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c1, c2}}
	catchAll := []config.Job{{ID: "r1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newScanOnceApp(t, fake, catchAll)

	a.setPostActionDeadline("app-one", time.Now().Add(5*time.Minute))

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 1 || restarts[0] != "c2" {
		t.Fatalf("expected only c2 restarted (c1 in cooldown), got %v", restarts)
	}
}

func TestScanOnceSkipsExhaustedContainerContinuesOthers(t *testing.T) {
	c1 := containertypes.Summary{ID: "c1", Names: []string{"app-one"}}
	c2 := containertypes.Summary{ID: "c2", Names: []string{"app-two"}}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c1, c2}}
	catchAll := []config.Job{{ID: "r1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newScanOnceApp(t, fake, catchAll)

	a.setRetriesExhausted("app-one", true)

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 1 || restarts[0] != "c2" {
		t.Fatalf("expected only c2 restarted (c1 exhausted), got %v", restarts)
	}
}

// Regression: after exhaustion, the next scan must NOT write a "cooldown active"
// history entry even if the postActionDeadline is still in the future.
// The exhausted check must run before the cooldown check.
func TestScanOnceExhaustedBeforeCooldownNoSpuriousHistory(t *testing.T) {
	c1 := containertypes.Summary{ID: "c1", Names: []string{"app-one"}}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c1}}
	catchAll := []config.Job{{ID: "r1", Name: "catch-all", Action: "restart", Enabled: true, ContainerNameFilter: "app-one"}}
	a, pool := newScanOnceApp(t, fake, catchAll)

	dir := t.TempDir()
	store, err := history.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	a.history = store

	// Mark exhausted AND still in cooldown — both conditions present simultaneously.
	a.setRetriesExhausted("app-one", true)
	a.setPostActionDeadline("app-one", time.Now().Add(5*time.Minute))

	a.scanOnce()
	pool.Stop()

	entries, err := store.List(50)
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	for _, e := range entries {
		if e.Error == "cooldown active" {
			t.Errorf("exhausted container wrote spurious 'cooldown active' history entry: %+v", e)
		}
	}
}

// Regression: a managed container briefly in health=starting after a restart must
// not trigger the recovery check. The cycle counter should only reset once the
// container is fully absent from all problematic lists.
func TestScanOnceStartingContainerBlocksRecovery(t *testing.T) {
	c1 := containertypes.Summary{ID: "c1", Names: []string{"app-one"}}
	fake := &fakeDockerClient{
		// Container is healthy enough to leave the unhealthy list ...
		unhealthy: []containertypes.Summary{},
		// ... but Docker reports its healthcheck is still initialising.
		starting: []containertypes.Summary{c1},
	}
	catchAll := []config.Job{{ID: "r1", Name: "catch-all", Action: "restart", Enabled: true, ContainerNameFilter: "app-one"}}
	a, pool := newScanOnceApp(t, fake, catchAll)

	// Simulate an in-progress restart: cycle=2, deadline already expired.
	a.actionCycle["app-one"] = 2
	a.postActionDeadline["app-one"] = time.Now().Add(-1 * time.Second)

	a.scanOnce()
	pool.Stop()

	a.mu.RLock()
	cycle := a.actionCycle["app-one"]
	a.mu.RUnlock()

	if cycle != 2 {
		t.Errorf("expected cycle to stay at 2 while container is health=starting, got %d", cycle)
	}
}

func TestScanOnceOneJobPerContainerPerCycle(t *testing.T) {
	c1 := containertypes.Summary{ID: "c1", Names: []string{"app-one"}}
	c2 := containertypes.Summary{ID: "c2", Names: []string{"app-two"}}
	fake := &fakeDockerClient{unhealthy: []containertypes.Summary{c1, c2}}
	catchAll := []config.Job{{ID: "r1", Name: "catch-all", Action: "restart", Enabled: true}}
	a, pool := newScanOnceApp(t, fake, catchAll)

	a.scanOnce()
	pool.Stop()

	fake.mu.Lock()
	restarts := append([]string(nil), fake.restartIDs...)
	fake.mu.Unlock()

	if len(restarts) != 2 {
		t.Fatalf("expected exactly 2 restarts (one per container per cycle), got %d: %v", len(restarts), restarts)
	}
}

func TestHandleSettingsReloadsRuntimeAndWorkerPool(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("APP_PATH", configDir)

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = loaded.HttpMaxIdleConns
	transport.MaxIdleConnsPerHost = loaded.HttpMaxIdleConnsPerHost
	transport.IdleConnTimeout = time.Duration(loaded.HttpIdleConnTimeoutSeconds) * time.Second

	a := &app{
		cfg:           loaded,
		notifier:      notify.New("discord://token@chan"),
		notifications: []config.NotificationProfile{{ID: "p1", Name: "test", Service: "discord://token@chan", Enabled: true}},
		httpClient:    &http.Client{Timeout: time.Duration(loaded.HttpClientTimeoutSeconds) * time.Second, Transport: transport},
		pool:          worker.NewPool(loaded.WorkerCount, loaded.QueueSize),
	}
	defer a.stopPool()
	oldPool := a.pool

	body := []byte(`{"retryCount":4,"retryDelaySeconds":6,"workerCount":4,"queueSize":96,"httpClientTimeoutSeconds":19,"httpMaxIdleConns":30,"httpMaxIdleConnsPerHost":14,"httpIdleConnTimeoutSeconds":99,"dockerPingTimeoutSeconds":8,"dockerClientRetryCount":2,"dockerClientRetryDelaySeconds":3,"actionTimeoutSeconds":29,"notificationRatePerSec":7,"action":"restart","externalHostname":"pool.example","runScriptPath":"/config/scripts/heal.sh","startExited":true,"requireFilterForExited":true,"sqlitePath":"/config/data/app.db","sqliteBusyTimeoutSeconds":6000,"sqlitePragmas":"journal_mode=WAL;synchronous=NORMAL"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.handleSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings status = %d body=%s", rr.Code, rr.Body.String())
	}

	cfg := a.getConfig()
	if cfg.WorkerCount != 4 || cfg.QueueSize != 96 {
		t.Fatalf("worker config not reloaded: %#v", cfg)
	}
	if a.pool == oldPool {
		t.Fatal("expected worker pool to be replaced when worker config changes")
	}
	if a.httpClient.Timeout != 19*time.Second {
		t.Fatalf("http timeout not reloaded: %v", a.httpClient.Timeout)
	}
	transportAfter, ok := a.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("http transport type assertion failed")
	}
	if transportAfter.MaxIdleConns != 30 || transportAfter.MaxIdleConnsPerHost != 14 || transportAfter.IdleConnTimeout != 99*time.Second {
		t.Fatalf("http pool settings not reloaded: %#v", transportAfter)
	}
	n, ok := a.notifier.(*notify.Notifier)
	if !ok {
		t.Fatal("notifier type assertion failed")
	}
	if !n.Enabled || len(n.Services) != 1 || n.Services[0] != "discord://token@chan" {
		t.Fatalf("notifier not reloaded: %#v", n)
	}
}
