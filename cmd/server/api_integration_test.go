package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chowkidar/internal/config"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
	"chowkidar/internal/worker"
)

func newIntegrationTestServer(t *testing.T) (*app, *httptest.Server) {
	t.Helper()

	configDir := t.TempDir()
	t.Setenv("APP_PATH", configDir)
	cfg := config.Config{
		Port:                          "8080",
		ConfigDir:                     configDir,
		Action:                        "restart",
		WorkerCount:                   2,
		QueueSize:                     16,
		HttpClientTimeoutSeconds:      15,
		HttpMaxIdleConns:              20,
		HttpMaxIdleConnsPerHost:       10,
		HttpIdleConnTimeoutSeconds:    90,
		DockerPingTimeoutSeconds:      5,
		DockerClientRetryCount:        1,
		DockerClientRetryDelaySeconds: 2,
		ActionTimeoutSeconds:          20,
		RequireFilterForExited:        true,
	}

	if err := config.SaveFileConfig(configDir, config.ToFileConfig(cfg)); err != nil {
		t.Fatalf("SaveFileConfig() error = %v", err)
	}

	historyStore, err := history.NewStore(configDir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = cfg.HttpMaxIdleConns
	transport.MaxIdleConnsPerHost = cfg.HttpMaxIdleConnsPerHost
	transport.IdleConnTimeout = time.Duration(cfg.HttpIdleConnTimeoutSeconds) * time.Second

	a := &app{
		cfg:           cfg,
		docker:        &fakeDockerClient{},
		notifier:      notify.New(""),
		pool:          worker.NewPool(cfg.WorkerCount, cfg.QueueSize),
		history:       historyStore,
		httpClient:    &http.Client{Timeout: time.Duration(cfg.HttpClientTimeoutSeconds) * time.Second, Transport: transport},
		jobs:          []config.Job{},
		notifications: []config.NotificationProfile{},
		scripts:       []config.ScriptEntry{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/settings", a.handleSettings)
	mux.HandleFunc("/api/jobs", a.handleJobs)
	mux.HandleFunc("/api/jobs/", a.handleJobByID)
	mux.HandleFunc("/api/notifications", a.handleNotifications)
	mux.HandleFunc("/api/docker-hosts", a.handleDockerHosts)
	mux.HandleFunc("/api/history", a.handleHistory)
	mux.HandleFunc("/api/health", a.handleHealth)

	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		a.stopPool()
	})
	return a, srv
}

func mustHTTPStatus(t *testing.T, method, url string, body []byte, wantStatus int) *http.Response {
	t.Helper()
	var req *http.Request
	if body != nil {
		req, _ = http.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(method, url, http.NoBody)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, url, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, url, resp.StatusCode, wantStatus)
	}
	return resp
}

func TestAPIIntegrationSettingsJobsHistory(t *testing.T) {
	a, srv := newIntegrationTestServer(t)

	resp := mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/settings", nil, http.StatusOK)
	_ = resp.Body.Close()

	settingsBody := []byte(`{"retryCount":4,"retryDelaySeconds":7,"workerCount":3,"queueSize":64,"httpClientTimeoutSeconds":19,"httpMaxIdleConns":25,"httpMaxIdleConnsPerHost":15,"httpIdleConnTimeoutSeconds":95,"dockerPingTimeoutSeconds":8,"dockerClientRetryCount":2,"dockerClientRetryDelaySeconds":4,"actionTimeoutSeconds":35,"notificationRatePerSec":9,"action":"restart","externalHostname":"pool.example","runScriptPath":"/config/scripts/heal.sh","startExited":true,"requireFilterForExited":true,"sqlitePath":"/config/data/app.db","sqliteBusyTimeoutSeconds":6000,"sqlitePragmas":"journal_mode=WAL;synchronous=NORMAL"}`)
	resp = mustHTTPStatus(t, http.MethodPut, srv.URL+"/api/settings", settingsBody, http.StatusOK)
	_ = resp.Body.Close()

	jobBody := []byte(`{"name":"integration-job","action":"restart","containerNameFilter":"demo","enabled":true,"startExited":true}`)
	resp = mustHTTPStatus(t, http.MethodPost, srv.URL+"/api/jobs", jobBody, http.StatusCreated)
	var created config.Job
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created job error = %v", err)
	}
	_ = resp.Body.Close()

	resp = mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/jobs", nil, http.StatusOK)
	var jobsOut struct {
		Jobs []config.Job `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jobsOut); err != nil {
		t.Fatalf("decode jobs error = %v", err)
	}
	_ = resp.Body.Close()
	if len(jobsOut.Jobs) != 1 || !jobsOut.Jobs[0].StartExited {
		t.Fatalf("unexpected jobs payload: %#v", jobsOut)
	}

	if err := a.history.Append(history.Entry{
		Timestamp:     time.Now().UTC(),
		ContainerID:   "c1",
		ContainerName: "demo",
		Reason:        "unhealthy",
		Action:        "restart",
		Attempt:       1,
		Status:        "success",
	}); err != nil {
		t.Fatalf("history append error = %v", err)
	}

	resp = mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/history?limit=1", nil, http.StatusOK)
	var historyOut struct {
		Entries []history.Entry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&historyOut); err != nil {
		t.Fatalf("decode history error = %v", err)
	}
	_ = resp.Body.Close()
	if len(historyOut.Entries) != 1 || historyOut.Entries[0].ContainerName != "demo" {
		t.Fatalf("unexpected history payload: %#v", historyOut)
	}

	resp = mustHTTPStatus(t, http.MethodDelete, srv.URL+"/api/jobs/"+created.ID, nil, http.StatusOK)
	_ = resp.Body.Close()
}

func TestFrontendSmokeFlowLoadSaveJobDeleteHistory(t *testing.T) {
	_, srv := newIntegrationTestServer(t)

	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatalf("frontend load health error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("frontend load health status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/settings")
	if err != nil {
		t.Fatalf("frontend load settings error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("frontend load settings status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	settingsBody := []byte(`{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":32,"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,"action":"restart","externalHostname":"frontend-smoke","runScriptPath":"","startExited":false,"requireFilterForExited":true,"sqlitePath":"/config/data/app.db","sqliteBusyTimeoutSeconds":5000,"sqlitePragmas":"journal_mode=WAL;synchronous=NORMAL"}`)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", bytes.NewReader(settingsBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("frontend save settings error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("frontend save settings status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	jobBody := []byte(`{"name":"frontend-smoke-job","action":"restart","containerNameFilter":"frontend","enabled":true,"startExited":false}`)
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/jobs", bytes.NewReader(jobBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("frontend add job error = %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("frontend add job status = %d", resp.StatusCode)
	}
	var created config.Job
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode frontend created job error = %v", err)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/jobs/"+created.ID, http.NoBody)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("frontend delete job error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("frontend delete job status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/history?limit=20")
	if err != nil {
		t.Fatalf("frontend load history error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("frontend load history status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestAPIIntegrationNotificationsAndDockerHosts(t *testing.T) {
	_, srv := newIntegrationTestServer(t)

	notifyBody := []byte(`{"profiles":[{"id":"p1","name":"Discord","provider":"discord","details":{"token":"token","webhookid":"id"},"service":"discord://token@id","enabled":true}]}`)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", bytes.NewReader(notifyBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/notifications error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/notifications status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	hostsBody := []byte(`{"profiles":[{"id":"h1","name":"Local","type":"socket","endpoint":"/var/run/docker.sock","enabled":true}],"activeHostID":"h1"}`)
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/api/docker-hosts", bytes.NewReader(hostsBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/docker-hosts error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/docker-hosts status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/docker-hosts")
	if err != nil {
		t.Fatalf("GET /api/docker-hosts error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/docker-hosts status = %d", resp.StatusCode)
	}
	var out struct {
		Profiles []config.DockerHostProfile `json:"profiles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode /api/docker-hosts error = %v", err)
	}
	_ = resp.Body.Close()
	// GET always prepends the built-in "local" entry, so expect 2 profiles total.
	found := false
	for _, p := range out.Profiles {
		if p.ID == "h1" {
			found = true
			break
		}
	}
	if len(out.Profiles) != 2 || !found {
		t.Fatalf("unexpected /api/docker-hosts payload: %#v", out)
	}
}
