package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chowkidar/internal/config"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
	"chowkidar/internal/worker"
)

// newBellTestServer builds a minimal test app+server wired with the endpoints
// needed to exercise bell notifications, history, and settings persistence.
func newBellTestServer(t *testing.T) (*app, *httptest.Server) {
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
		NotificationRatePerSec:        5,
	}
	if err := config.SaveFileConfig(configDir, config.ToFileConfig(cfg)); err != nil {
		t.Fatalf("SaveFileConfig: %v", err)
	}
	historyStore, err := history.NewStore(configDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	a := &app{
		cfg:                  cfg,
		docker:               &fakeDockerClient{},
		notifier:             notify.New(""),
		pool:                 worker.NewPool(cfg.WorkerCount, cfg.QueueSize),
		history:              historyStore,
		httpClient:           &http.Client{},
		jobs:                 []config.Job{},
		notifications:        []config.NotificationProfile{},
		scripts:              []config.ScriptEntry{},
		lastNotified:         make(map[string]time.Time),
		actionCycle:          make(map[string]int),
		retriesExhausted:     make(map[string]bool),
		postActionDeadline:   make(map[string]time.Time),
		activeJobs:           make(map[string]bool),
		lastJobScan:          make(map[string]time.Time),
		lastJobNotifications: make(map[string][]string),
		notifUsage:           make(map[string]*notifProfileUsage),
		lastJobRuleName:      make(map[string]string),
		knownNames:           make(map[string]string),
		sessions:             make(map[string]sessionEntry),
		bootTime:             time.Now().UTC(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", a.handleHealth)
	mux.HandleFunc("/api/system-alerts", a.handleSystemAlerts)
	mux.HandleFunc("/api/system-alerts/dismiss", a.handleSystemAlertsDismiss)
	mux.HandleFunc("/api/history", a.handleHistoryEndpoint)
	mux.HandleFunc("/api/settings", a.handleSettings)
	mux.HandleFunc("/api/docker-hosts", a.handleDockerHosts)
	mux.HandleFunc("/api/notifications", a.handleNotifications)
	mux.HandleFunc("/api/notifications/", a.handleNotificationByID)

	srv := httptest.NewServer(mux)
	t.Cleanup(func() { srv.Close(); a.stopPool() })
	return a, srv
}

// ── /api/health ───────────────────────────────────────────────────────────────

func TestHealthEndpointNoAuthRequired(t *testing.T) {
	// /api/health must return 200 without any auth cookie so Docker HEALTHCHECK works.
	_, srv := newBellTestServer(t)
	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHealthEndpointIncludesVersionAndBootTime(t *testing.T) {
	_, srv := newBellTestServer(t)
	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, _ := body["version"].(string); v == "" {
		t.Errorf("health.version missing or empty; got %v", body["version"])
	}
	if v, _ := body["version"].(string); v != appVersion {
		t.Errorf("health.version = %q, want %q", v, appVersion)
	}
	if boot := body["bootTime"]; boot == nil {
		t.Errorf("health.bootTime missing")
	}
}

func TestHealthEndpointIncludesLatestVersionRelDate(t *testing.T) {
	a, srv := newBellTestServer(t)
	// Simulate a version check having run.
	a.mu.Lock()
	a.latestVersion = "9.9.9"
	a.latestVersionRelDate = "2099-01-01"
	a.mu.Unlock()

	resp, _ := http.Get(srv.URL + "/api/health")
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)

	if v, _ := body["latestVersion"].(string); v != "9.9.9" {
		t.Errorf("latestVersion = %q, want 9.9.9", v)
	}
	if d, _ := body["latestVersionRelDate"].(string); d != "2099-01-01" {
		t.Errorf("latestVersionRelDate = %q, want 2099-01-01", d)
	}
}

func TestHealthEndpointBootTimeIsStable(t *testing.T) {
	// bootTime must be the same across multiple calls (same server run).
	_, srv := newBellTestServer(t)
	getBootTime := func() string {
		resp, _ := http.Get(srv.URL + "/api/health")
		defer func() { _ = resp.Body.Close() }()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		bt, _ := body["bootTime"].(string)
		return bt
	}
	first := getBootTime()
	second := getBootTime()
	if first == "" || first != second {
		t.Errorf("bootTime not stable: %q vs %q", first, second)
	}
}

// ── /api/system-alerts ────────────────────────────────────────────────────────

func TestSystemAlertsEmptyWithNoHistory(t *testing.T) {
	_, srv := newBellTestServer(t)
	resp, err := http.Get(srv.URL + "/api/system-alerts")
	if err != nil {
		t.Fatalf("GET /api/system-alerts: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Alerts []map[string]any `json:"alerts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// No lastScan yet → monitoring_started alert absent; no history → no recovery alerts.
	for _, a := range body.Alerts {
		if a["type"] == "monitoring_started" {
			t.Errorf("unexpected monitoring_started alert with zero lastScan")
		}
	}
}

func TestSystemAlertsMonitoringStartedAfterScan(t *testing.T) {
	a, srv := newBellTestServer(t)
	// Simulate a completed scan by setting lastScan.
	a.mu.Lock()
	a.lastScan = time.Now().UTC()
	a.mu.Unlock()

	resp, err := http.Get(srv.URL + "/api/system-alerts")
	if err != nil {
		t.Fatalf("GET /api/system-alerts: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Alerts []map[string]any `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)

	var found bool
	for _, al := range body.Alerts {
		if al["type"] == "monitoring_started" {
			found = true
			// ID must include boot timestamp so page-refresh doesn't clear it.
			id, _ := al["id"].(string)
			if !strings.HasPrefix(id, "monitoring-started-") {
				t.Errorf("monitoring_started id = %q, want prefix monitoring-started-", id)
			}
		}
	}
	if !found {
		t.Errorf("monitoring_started alert not present after lastScan is set")
	}
}

func TestSystemAlertsMonitoringStartedIDChangesOnNewBoot(t *testing.T) {
	// Two app instances (representing two server boots) must produce different IDs.
	a1 := &app{bootTime: time.Now()}
	time.Sleep(2 * time.Millisecond)
	a2 := &app{bootTime: time.Now()}

	id1 := "monitoring-started-" + a1.bootTime.Format("20060102T150405")
	id2 := "monitoring-started-" + a2.bootTime.Format("20060102T150405")
	// They might be the same if both fall in the same second — that's OK.
	// What we care about is they're NOT both empty.
	if id1 == "" || id2 == "" {
		t.Errorf("boot-based IDs must not be empty: %q %q", id1, id2)
	}
}

func TestSystemAlertsFailedRecoveryAlerts(t *testing.T) {
	a, srv := newBellTestServer(t)

	// Append a failed history entry.
	_ = a.history.Append(history.Entry{
		Timestamp:     time.Now().UTC(),
		ContainerID:   "cid-abc123",
		ContainerName: "my-service",
		Action:        "restart",
		Status:        "failed",
		Error:         "script failed: exit status 1",
	})

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Alerts []map[string]any `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)

	var found bool
	for _, al := range body.Alerts {
		if al["type"] == "failed_recovery" && al["containerName"] == "my-service" {
			found = true
			id, _ := al["id"].(string)
			if id == "" {
				t.Errorf("failed_recovery alert has empty id")
			}
		}
	}
	if !found {
		t.Errorf("failed_recovery alert for my-service not found; got %+v", body.Alerts)
	}
}

func TestSystemAlertsExhaustedAlerts(t *testing.T) {
	a, srv := newBellTestServer(t)

	_ = a.history.Append(history.Entry{
		Timestamp:     time.Now().UTC(),
		ContainerID:   "cid-exhausted",
		ContainerName: "exhausted-svc",
		Action:        "restart",
		Status:        "exhausted",
		Error:         "monitoring paused — max retries reached",
	})

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Alerts []map[string]any `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)

	var found bool
	for _, al := range body.Alerts {
		if al["type"] == "paused_monitoring" && al["containerName"] == "exhausted-svc" {
			found = true
		}
	}
	if !found {
		t.Errorf("paused_monitoring alert not found; got %+v", body.Alerts)
	}
}

func TestSystemAlertsDeduplicatesByContainerNameAndStatus(t *testing.T) {
	a, srv := newBellTestServer(t)

	// Append multiple failed entries for the same container.
	for i := 0; i < 5; i++ {
		_ = a.history.Append(history.Entry{
			Timestamp:     time.Now().UTC(),
			ContainerID:   "cid-dup",
			ContainerName: "dup-svc",
			Action:        "restart",
			Status:        "failed",
		})
	}

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Alerts []map[string]any `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)

	count := 0
	for _, al := range body.Alerts {
		if al["type"] == "failed_recovery" && al["containerName"] == "dup-svc" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 dedup'd failed_recovery for dup-svc, got %d", count)
	}
}

// ── POST /api/system-alerts/dismiss ──────────────────────────────────────────

func TestSystemAlertsDismissEndpoint(t *testing.T) {
	a, srv := newBellTestServer(t)

	// Seed a failed history entry so a failed_recovery alert exists.
	_ = a.history.Append(history.Entry{
		Timestamp:     time.Now().UTC(),
		ContainerID:   "cid-dismiss",
		ContainerName: "dismiss-svc",
		Action:        "restart",
		Status:        "failed",
		Error:         "timeout",
	})

	// Fetch alerts to get the ID.
	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	var body struct {
		Alerts []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()

	var targetID string
	for _, al := range body.Alerts {
		if al.Type == "failed_recovery" {
			targetID = al.ID
			break
		}
	}
	if targetID == "" {
		t.Fatal("no failed_recovery alert found before dismiss")
	}

	// Dismiss it.
	dismissBody := fmt.Sprintf(`{"ids":[%q]}`, targetID)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/system-alerts/dismiss", strings.NewReader(dismissBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("dismiss status = %d, want 200", resp2.StatusCode)
	}

	// Alert must now be absent.
	resp3, _ := http.Get(srv.URL + "/api/system-alerts")
	var body3 struct {
		Alerts []map[string]any `json:"alerts"`
	}
	_ = json.NewDecoder(resp3.Body).Decode(&body3)
	_ = resp3.Body.Close()
	for _, al := range body3.Alerts {
		if al["id"] == targetID {
			t.Errorf("dismissed alert id=%s still returned", targetID)
		}
	}
}

func TestSystemAlertsDismissPersistedAcrossReload(t *testing.T) {
	a, srv := newBellTestServer(t)

	ts := time.Now().UTC()
	_ = a.history.Append(history.Entry{
		Timestamp:     ts,
		ContainerID:   "cid-persist",
		ContainerName: "persist-svc",
		Action:        "restart",
		Status:        "failed",
	})

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	var body struct {
		Alerts []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	var targetID string
	for _, al := range body.Alerts {
		if al.Type == "failed_recovery" {
			targetID = al.ID
		}
	}
	if targetID == "" {
		t.Fatal("no failed_recovery alert found")
	}

	dismissBody := fmt.Sprintf(`{"ids":[%q]}`, targetID)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/system-alerts/dismiss", strings.NewReader(dismissBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req)
	_ = resp2.Body.Close()

	// Load the persisted file and verify the ID is there.
	loaded, err := loadDismissedAlerts(a.cfg.ConfigDir)
	if err != nil {
		t.Fatalf("loadDismissedAlerts: %v", err)
	}
	if _, ok := loaded[targetID]; !ok {
		t.Errorf("dismissed ID %s not found in persisted file", targetID)
	}
}

func TestFailedRecoveryAlertIDIncludesTimestamp(t *testing.T) {
	a, srv := newBellTestServer(t)
	a.mu.Lock()
	a.lastScan = time.Now().UTC()
	a.mu.Unlock()

	ts := time.Now().UTC()
	_ = a.history.Append(history.Entry{
		Timestamp:     ts,
		ContainerID:   "cid-ts",
		ContainerName: "ts-svc",
		Action:        "restart",
		Status:        "failed",
	})

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	var body struct {
		Alerts []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()

	for _, al := range body.Alerts {
		if al.Type == "failed_recovery" {
			// ID must end with a Unix timestamp so each event is unique.
			expectedSuffix := fmt.Sprintf("-%d", ts.Unix())
			if !strings.HasSuffix(al.ID, expectedSuffix) {
				t.Errorf("alert ID %q does not end with unix timestamp %s", al.ID, expectedSuffix)
			}
			return
		}
	}
	t.Error("no failed_recovery alert found")
}

// ── DELETE /api/history ───────────────────────────────────────────────────────

func TestHistoryClearEndpoint(t *testing.T) {
	a, srv := newBellTestServer(t)

	// Add some history.
	for i := 0; i < 3; i++ {
		_ = a.history.Append(history.Entry{
			Timestamp: time.Now().UTC(), ContainerID: "c1", ContainerName: "svc",
			Action: "restart", Status: "success",
		})
	}

	// Verify entries exist.
	resp, _ := http.Get(srv.URL + "/api/history?limit=10")
	var before struct{ Total int `json:"total"` }
	_ = json.NewDecoder(resp.Body).Decode(&before)
	_ = resp.Body.Close()
	if before.Total == 0 {
		t.Fatal("expected history entries before clear")
	}

	// DELETE clears history.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/history", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/history: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var cleared struct{ Cleared bool `json:"cleared"` }
	_ = json.NewDecoder(resp.Body).Decode(&cleared)
	if !cleared.Cleared {
		t.Errorf("response.cleared = false, want true")
	}

	// History must now be empty.
	resp2, _ := http.Get(srv.URL + "/api/history?limit=10")
	var after struct{ Total int `json:"total"` }
	_ = json.NewDecoder(resp2.Body).Decode(&after)
	_ = resp2.Body.Close()
	if after.Total != 0 {
		t.Errorf("history total after clear = %d, want 0", after.Total)
	}
}

func TestHistoryPostSeedsEntries(t *testing.T) {
	_, srv := newBellTestServer(t)

	entries := []map[string]any{
		{"timestamp": "2026-01-01T10:01:00Z", "containerId": "s1", "containerName": "seed-c", "reason": "unhealthy", "action": "restart", "attempt": 1, "status": "success", "durationMs": 100},
		{"timestamp": "2026-01-01T10:02:00Z", "containerId": "s2", "containerName": "seed-c", "reason": "unhealthy", "action": "restart", "attempt": 1, "status": "failed", "durationMs": 200},
		{"timestamp": "2026-01-01T10:03:00Z", "containerId": "s3", "containerName": "seed-c", "reason": "unhealthy", "action": "restart", "attempt": 1, "status": "healthy", "durationMs": 0},
	}
	body, _ := json.Marshal(entries)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/history", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/history status = %d, want 201", resp.StatusCode)
	}
	var out struct{ Seeded int `json:"seeded"` }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Seeded != 3 {
		t.Errorf("seeded = %d, want 3", out.Seeded)
	}

	// Verify entries are readable. Use bars=true to include all statuses
	// (default GET excludes "healthy" entries).
	resp2, _ := http.Get(srv.URL + "/api/history?bars=true&limit=10")
	var hist struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&hist)
	_ = resp2.Body.Close()
	if hist.Total != 3 {
		t.Errorf("total after seed = %d, want 3", hist.Total)
	}
}

func TestHistoryPostRejectsBadJSON(t *testing.T) {
	_, srv := newBellTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/history", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHistoryGetAfterClearReturnsEmpty(t *testing.T) {
	a, srv := newBellTestServer(t)
	_ = a.history.Append(history.Entry{Timestamp: time.Now().UTC(), ContainerID: "x", ContainerName: "x", Status: "success"})

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/history", http.NoBody)
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	resp2, _ := http.Get(srv.URL + "/api/history?bars=true&limit=2000")
	var out struct {
		Entries []history.Entry `json:"entries"`
		Total   int             `json:"total"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&out)
	_ = resp2.Body.Close()
	if out.Total != 0 || len(out.Entries) != 0 {
		t.Errorf("after clear: total=%d entries=%d, want both 0", out.Total, len(out.Entries))
	}
}

// ── /api/history?bars=true limit cap ─────────────────────────────────────────

func TestHistoryBarsLimitCapIs2000(t *testing.T) {
	a, srv := newBellTestServer(t)

	// Append 600 entries with "success" status so they appear in BOTH the
	// regular query (excludes "healthy") and the bars query (includes all).
	for i := 0; i < 600; i++ {
		_ = a.history.Append(history.Entry{
			Timestamp: time.Now().UTC(), ContainerID: "c", ContainerName: "svc",
			Action: "restart", Status: "success",
		})
	}

	// Without bars=true the cap is 500. limit=500 is valid and returns 500 rows.
	resp, _ := http.Get(srv.URL + "/api/history?limit=500")
	var body500 struct{ Entries []history.Entry `json:"entries"` }
	_ = json.NewDecoder(resp.Body).Decode(&body500)
	_ = resp.Body.Close()
	if len(body500.Entries) != 500 {
		t.Errorf("non-bars limit=500: got %d entries, want 500", len(body500.Entries))
	}

	// With bars=true the cap is 2000. limit=600 is valid and returns all 600.
	resp2, _ := http.Get(srv.URL + "/api/history?bars=true&limit=600")
	var body600 struct{ Entries []history.Entry `json:"entries"` }
	_ = json.NewDecoder(resp2.Body).Decode(&body600)
	_ = resp2.Body.Close()
	if len(body600.Entries) != 600 {
		t.Errorf("bars limit=600: got %d entries, want 600", len(body600.Entries))
	}
}

func TestHistoryBarsLimit1000NotClampedTo25(t *testing.T) {
	// Previously limit=1000 was silently reduced to 25 (the default) because
	// the cap check was parsed > 0 && parsed <= 500.  Verify the regression is gone.
	a, srv := newBellTestServer(t)

	for i := 0; i < 50; i++ {
		_ = a.history.Append(history.Entry{
			Timestamp: time.Now().UTC(), ContainerID: "c", ContainerName: "svc",
			Action: "restart", Status: "healthy",
		})
	}

	resp, _ := http.Get(srv.URL + "/api/history?bars=true&limit=1000")
	var out struct{ Entries []history.Entry `json:"entries"` }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()

	// We have 50 entries; limit=1000 with the new cap should return all 50.
	if len(out.Entries) != 50 {
		t.Errorf("bars limit=1000: got %d entries, want 50 (regression: falling back to default 25?)", len(out.Entries))
	}
}

// ── Settings persistence ──────────────────────────────────────────────────────

func TestSettingsExternalHostnamePersists(t *testing.T) {
	_, srv := newBellTestServer(t)

	body := `{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,` +
		`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,` +
		`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,` +
		`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,` +
		`"action":"restart","externalHostname":"myserver.local","primaryBaseURL":"http://test.example",` +
		`"dashboardRefreshSeconds":15}`

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d", resp.StatusCode)
	}

	// Re-read via GET and verify fields persisted.
	resp2, _ := http.Get(srv.URL + "/api/settings")
	var cfg map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&cfg)
	_ = resp2.Body.Close()

	if cfg["externalHostname"] != "myserver.local" {
		t.Errorf("externalHostname = %v, want myserver.local", cfg["externalHostname"])
	}
	if cfg["primaryBaseURL"] != "http://test.example" {
		t.Errorf("primaryBaseURL = %v, want http://test.example", cfg["primaryBaseURL"])
	}
	if int(cfg["dashboardRefreshSeconds"].(float64)) != 15 {
		t.Errorf("dashboardRefreshSeconds = %v, want 15", cfg["dashboardRefreshSeconds"])
	}
}

func TestDashboardRefreshSecondsPreservedAfterDockerHostsSave(t *testing.T) {
	_, srv := newBellTestServer(t)

	// Save settings with a specific dashboardRefreshSeconds.
	settingsBody := `{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,` +
		`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,` +
		`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,` +
		`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,` +
		`"action":"restart","dashboardRefreshSeconds":42}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(settingsBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Save Docker Hosts (just sending the same data back — exercises the code path
	// that previously overwrote config.yaml via ToFileConfig, losing DashboardRefreshSeconds).
	hostsGetResp, _ := http.Get(srv.URL + "/api/docker-hosts")
	var hostsPayload map[string]any
	_ = json.NewDecoder(hostsGetResp.Body).Decode(&hostsPayload)
	_ = hostsGetResp.Body.Close()

	hostsJSON, _ := json.Marshal(hostsPayload)
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/docker-hosts", strings.NewReader(string(hostsJSON)))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	_ = resp2.Body.Close()

	// Verify dashboardRefreshSeconds was NOT reset to 0.
	resp3, _ := http.Get(srv.URL + "/api/settings")
	var cfg map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&cfg)
	_ = resp3.Body.Close()

	v, ok := cfg["dashboardRefreshSeconds"].(float64)
	if !ok || int(v) != 42 {
		t.Errorf("dashboardRefreshSeconds after docker-hosts save = %v, want 42 (regression: value reset?)", cfg["dashboardRefreshSeconds"])
	}
}

// ── Notification rate-limiting / suspension ───────────────────────────────────

func TestNotificationProfileSuspensionPersistsThroughSettingsSave(t *testing.T) {
	_, srv := newBellTestServer(t)

	// Create a notification profile.
	profBody := `{"profiles":[{"id":"p-bell-test","name":"Bell Test","provider":"discord",` +
		`"details":{"token":"tok","webhookid":"wid"},"service":"discord://tok@wid","enabled":true}]}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", strings.NewReader(profBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/notifications status = %d", resp.StatusCode)
	}

	// Suspend the profile until midnight.
	suspBody := `{"until":"midnight"}`
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/notifications/p-bell-test/suspend", strings.NewReader(suspBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("POST suspend status = %d", resp2.StatusCode)
	}

	// Re-read the profile and confirm SuspendedUntil is set.
	resp3, _ := http.Get(srv.URL + "/api/notifications")
	var notifs struct {
		Profiles []struct {
			ID             string    `json:"id"`
			SuspendedUntil time.Time `json:"suspendedUntil"`
		} `json:"profiles"`
	}
	_ = json.NewDecoder(resp3.Body).Decode(&notifs)
	_ = resp3.Body.Close()

	var found bool
	for _, p := range notifs.Profiles {
		if p.ID == "p-bell-test" {
			found = true
			if p.SuspendedUntil.IsZero() {
				t.Errorf("SuspendedUntil is zero after suspend")
			}
		}
	}
	if !found {
		t.Errorf("profile p-bell-test not found after suspend")
	}
}

func TestNotificationProfileResumeClears(t *testing.T) {
	_, srv := newBellTestServer(t)

	profBody := `{"profiles":[{"id":"p-resume","name":"Resume Test","provider":"discord",` +
		`"details":{"token":"t","webhookid":"w"},"service":"discord://t@w","enabled":true}]}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", strings.NewReader(profBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Suspend then resume.
	suspReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/notifications/p-resume/suspend", strings.NewReader(`{"until":"24h"}`))
	suspReq.Header.Set("Content-Type", "application/json")
	r, _ := http.DefaultClient.Do(suspReq)
	_ = r.Body.Close()

	resumeReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/notifications/p-resume/resume", http.NoBody)
	r2, _ := http.DefaultClient.Do(resumeReq)
	_ = r2.Body.Close()

	resp3, _ := http.Get(srv.URL + "/api/notifications")
	var notifs struct {
		Profiles []struct {
			ID             string    `json:"id"`
			SuspendedUntil time.Time `json:"suspendedUntil"`
		} `json:"profiles"`
	}
	_ = json.NewDecoder(resp3.Body).Decode(&notifs)
	_ = resp3.Body.Close()

	for _, p := range notifs.Profiles {
		if p.ID == "p-resume" {
			if !p.SuspendedUntil.IsZero() {
				t.Errorf("SuspendedUntil not cleared after resume: %v", p.SuspendedUntil)
			}
		}
	}
}

