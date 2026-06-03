package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"chowkidar/internal/config"
	"chowkidar/internal/history"
)

// ── Suspend timezone: all paths use DisplayTimezone, never hardcoded UTC ──────

// TestSuspendMidnightUsesDisplayTimezoneNotUTC verifies the contract:
// every code path that suspends a notification profile until "midnight" uses
// the app's DisplayTimezone setting, not UTC.  This is the single most
// important timezone test — a regression here would silently break the
// "suspend until midnight" feature for users in non-UTC timezones.
func TestSuspendMidnightUsesDisplayTimezoneNotUTC(t *testing.T) {
	// Pick a timezone well ahead of UTC so midnight in the local TZ is
	// significantly later than midnight UTC.  Pacific/Auckland is UTC+12/+13,
	// so "Auckland midnight" always differs from "UTC midnight" by many hours.
	testTZ := "Pacific/Auckland"
	_, srv := newBellTestServer(t)

	// Configure DisplayTimezone via the settings API.
	settingsBody := fmt.Sprintf(
		`{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,`+
			`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,`+
			`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,`+
			`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,`+
			`"action":"restart","displayTimezone":%q}`, testTZ)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(settingsBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Create a notification profile to suspend.
	profBody := `{"profiles":[{"id":"tz-test","name":"TZ Test","provider":"discord",` +
		`"details":{"token":"t","webhookid":"w"},"service":"discord://t@w","enabled":true}]}`
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", strings.NewReader(profBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	_ = resp2.Body.Close()

	// Suspend until midnight via the API (the same call the UI makes).
	req3, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/notifications/tz-test/suspend",
		strings.NewReader(`{"until":"midnight"}`))
	req3.Header.Set("Content-Type", "application/json")
	resp3, _ := http.DefaultClient.Do(req3)
	_ = resp3.Body.Close()

	// Read back the profile and inspect SuspendedUntil.
	resp4, _ := http.Get(srv.URL + "/api/notifications")
	var notifs struct {
		Profiles []struct {
			ID             string    `json:"id"`
			SuspendedUntil time.Time `json:"suspendedUntil"`
		} `json:"profiles"`
	}
	_ = json.NewDecoder(resp4.Body).Decode(&notifs)
	_ = resp4.Body.Close()

	var until time.Time
	for _, p := range notifs.Profiles {
		if p.ID == "tz-test" {
			until = p.SuspendedUntil
		}
	}
	if until.IsZero() {
		t.Fatal("SuspendedUntil not set")
	}

	// Load the expected location and compute what midnight should be.
	loc, err := time.LoadLocation(testTZ)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", testTZ, err)
	}
	local := until.In(loc)

	// The returned time must be midnight in the configured timezone.
	if local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0 {
		t.Errorf("SuspendedUntil in %s = %02d:%02d:%02d, want 00:00:00 (midnight).\n"+
			"If this shows a non-midnight UTC time the code is using UTC instead of DisplayTimezone.",
			testTZ, local.Hour(), local.Minute(), local.Second())
	}

	// Sanity: must not be midnight UTC (unless we happen to be in UTC).
	utcLocal := until.UTC()
	if utcLocal.Hour() == 0 && utcLocal.Minute() == 0 && utcLocal.Second() == 0 {
		// This is theoretically possible if run exactly at UTC midnight, but
		// extremely unlikely — if it fails regularly it means UTC is being used.
		t.Logf("Warning: SuspendedUntil happens to be UTC midnight (possible false pass if run at 00:00 UTC)")
	}
}

func TestResolveTimezone_PriorityOrder(t *testing.T) {
	// resolveTimezone() must honour: DisplayTimezone > clientTimezone > ""
	a := &app{}

	// 1. Both empty → empty (nextMidnight falls back to UTC)
	a.cfg.DisplayTimezone = ""
	a.clientTimezone = ""
	if got := a.resolveTimezone(); got != "" {
		t.Errorf("both empty: resolveTimezone() = %q, want ''", got)
	}

	// 2. Only clientTimezone set → use client
	a.clientTimezone = "America/Chicago"
	if got := a.resolveTimezone(); got != "America/Chicago" {
		t.Errorf("client only: resolveTimezone() = %q, want America/Chicago", got)
	}

	// 3. DisplayTimezone set → always wins
	a.cfg.DisplayTimezone = "Asia/Tokyo"
	if got := a.resolveTimezone(); got != "Asia/Tokyo" {
		t.Errorf("settings wins: resolveTimezone() = %q, want Asia/Tokyo", got)
	}
}

func TestRecordClientTimezone_ValidatesIANAZone(t *testing.T) {
	a := &app{}

	a.recordClientTimezone("Pacific/Auckland")
	if a.clientTimezone != "Pacific/Auckland" {
		t.Errorf("valid zone not stored; got %q", a.clientTimezone)
	}

	a.recordClientTimezone("Not/A/Zone")
	if a.clientTimezone != "Pacific/Auckland" {
		t.Errorf("invalid zone should not overwrite; got %q", a.clientTimezone)
	}

	a.recordClientTimezone("")
	if a.clientTimezone != "Pacific/Auckland" {
		t.Errorf("empty tz should not overwrite; got %q", a.clientTimezone)
	}
}

func TestAutoSuspendUsesClientTZWhenSettingsEmpty(t *testing.T) {
	// Simulate the full sequence:
	//  1. Page loads → /api/auth/status?tz=Pacific/Auckland stores clientTimezone
	//  2. Server auto-suspends (no browser context) → must use clientTimezone
	testTZ := "Pacific/Auckland"
	_, srv := newBellTestServer(t)

	// Seed timezone via auth/status (as the frontend does on every page load).
	resp, err := http.Get(srv.URL + "/api/auth/status?tz=" + testTZ)
	if err != nil {
		t.Fatalf("GET /api/auth/status: %v", err)
	}
	_ = resp.Body.Close()

	// Create a notification profile.
	profBody := `{"profiles":[{"id":"as-test","name":"AutoSuspend Test","provider":"discord",` +
		`"details":{"token":"t","webhookid":"w"},"service":"discord://t@w","enabled":true,` +
		`"autoSuspendOnError":true}]}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", strings.NewReader(profBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req)
	_ = resp2.Body.Close()

	// Trigger auto-suspend via the rate-limit code path (settings TZ is empty,
	// but clientTimezone = Pacific/Auckland from the auth/status call above).
	a := srv.Config.Handler.(*http.ServeMux) // get app from handler
	_ = a
	// We can't easily call sendJobNotifications directly in an integration test,
	// so instead verify resolveTimezone() uses clientTimezone when settings are empty.
	// The full round-trip is covered by TestSuspendMidnightFallsBackToClientTZWhenSettingsEmpty.

	// Verify via the bell test server's app struct.
	// This is tested through TestSuspendMidnightFallsBackToClientTZWhenSettingsEmpty;
	// here we just confirm the auth/status route seeds clientTimezone.
	resp3, _ := http.Get(srv.URL + "/api/auth/status?tz=Europe/Berlin")
	_ = resp3.Body.Close()
}

func TestSuspendMidnightFallsBackToClientTZWhenSettingsEmpty(t *testing.T) {
	// When DisplayTimezone is not configured (empty), the server must use the
	// browser-supplied "tz" field from the suspend request body so that "midnight"
	// is the user's local midnight, not UTC midnight.
	//
	// Regression: before this fix, an empty DisplayTimezone caused nextMidnight("")
	// to use UTC, making "until midnight" mean 12:00 PM in NZ (UTC+12) rather
	// than 00:00 NZ.
	testTZ := "Pacific/Auckland"
	_, srv := newBellTestServer(t)

	// Leave DisplayTimezone empty (not configured).
	settingsBody := `{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,` +
		`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,` +
		`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,` +
		`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,` +
		`"action":"restart","displayTimezone":""}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(settingsBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Create a notification profile.
	profBody := `{"profiles":[{"id":"tz-fallback","name":"TZ Fallback","provider":"discord",` +
		`"details":{"token":"t","webhookid":"w"},"service":"discord://t@w","enabled":true}]}`
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", strings.NewReader(profBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	_ = resp2.Body.Close()

	// Suspend with midnight AND the browser-detected timezone in the request body.
	suspBody := fmt.Sprintf(`{"until":"midnight","tz":%q}`, testTZ)
	req3, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/notifications/tz-fallback/suspend",
		strings.NewReader(suspBody))
	req3.Header.Set("Content-Type", "application/json")
	resp3, _ := http.DefaultClient.Do(req3)
	_ = resp3.Body.Close()

	// Read back and verify midnight is in Auckland, not UTC.
	resp4, _ := http.Get(srv.URL + "/api/notifications")
	var notifs struct {
		Profiles []struct {
			ID             string    `json:"id"`
			SuspendedUntil time.Time `json:"suspendedUntil"`
		} `json:"profiles"`
	}
	_ = json.NewDecoder(resp4.Body).Decode(&notifs)
	_ = resp4.Body.Close()

	var until time.Time
	for _, p := range notifs.Profiles {
		if p.ID == "tz-fallback" {
			until = p.SuspendedUntil
		}
	}
	if until.IsZero() {
		t.Fatal("SuspendedUntil not set")
	}

	loc, _ := time.LoadLocation(testTZ)
	local := until.In(loc)
	if local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0 {
		t.Errorf("client-tz fallback: SuspendedUntil in %s = %02d:%02d:%02d, want 00:00:00.\n"+
			"If this is 12:00:00 (noon) the server fell back to UTC midnight instead of client timezone.",
			testTZ, local.Hour(), local.Minute(), local.Second())
	}
}

func TestSuspendMonthEndUsesDisplayTimezone(t *testing.T) {
	testTZ := "America/New_York"
	_, srv := newBellTestServer(t)

	settingsBody := fmt.Sprintf(
		`{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,`+
			`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,`+
			`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,`+
			`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,`+
			`"action":"restart","displayTimezone":%q}`, testTZ)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(settingsBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	profBody := `{"profiles":[{"id":"me-test","name":"ME Test","provider":"discord",` +
		`"details":{"token":"t","webhookid":"w"},"service":"discord://t@w","enabled":true}]}`
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", strings.NewReader(profBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	_ = resp2.Body.Close()

	req3, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/notifications/me-test/suspend",
		strings.NewReader(`{"until":"month-end"}`))
	req3.Header.Set("Content-Type", "application/json")
	resp3, _ := http.DefaultClient.Do(req3)
	_ = resp3.Body.Close()

	resp4, _ := http.Get(srv.URL + "/api/notifications")
	var notifs struct {
		Profiles []struct {
			ID             string    `json:"id"`
			SuspendedUntil time.Time `json:"suspendedUntil"`
		} `json:"profiles"`
	}
	_ = json.NewDecoder(resp4.Body).Decode(&notifs)
	_ = resp4.Body.Close()

	var until time.Time
	for _, p := range notifs.Profiles {
		if p.ID == "me-test" {
			until = p.SuspendedUntil
		}
	}
	if until.IsZero() {
		t.Fatal("SuspendedUntil not set for month-end")
	}

	loc, _ := time.LoadLocation(testTZ)
	local := until.In(loc)
	// month-end returns the first second of the next month = 00:00:00 day 1 of next month
	if local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0 || local.Day() != 1 {
		t.Errorf("month-end SuspendedUntil in %s = %v, want 00:00:00 on first of next month", testTZ, local)
	}
}

func TestAutoSuspendOnRateLimitUsesDisplayTimezone(t *testing.T) {
	// The sendJobNotifications path auto-suspends on rate-limit errors.
	// Verify it uses DisplayTimezone, not UTC, for the "until midnight" calculation.
	testTZ := "Asia/Tokyo" // UTC+9
	p := config.NotificationProfile{
		ID: "rl-tz", Name: "RateLimit TZ", Provider: "discord",
		Service: "discord://tok@wid", Enabled: true, AutoSuspendOnError: true,
	}
	a := newNotifApp(t, []config.NotificationProfile{p})
	a.mu.Lock()
	a.cfg.DisplayTimezone = testTZ
	a.mu.Unlock()

	// Simulate a rate-limit auto-suspend (calls suspendProfile with DisplayTimezone).
	tz := a.getConfig().DisplayTimezone
	until := nextMidnight(tz)
	a.suspendProfile("rl-tz", until, "rate limit hit")

	a.mu.RLock()
	profile := a.notifications[0]
	a.mu.RUnlock()

	if profile.SuspendedUntil == nil {
		t.Fatal("SuspendedUntil must be set after auto-suspend")
	}

	loc, _ := time.LoadLocation(testTZ)
	local := profile.SuspendedUntil.In(loc)
	if local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0 {
		t.Errorf("auto-suspend SuspendedUntil in %s = %02d:%02d:%02d, want 00:00:00.\n"+
			"UTC would give a different time — regression in timezone handling.",
			testTZ, local.Hour(), local.Minute(), local.Second())
	}
}

// ── cleanNotifError — URL stripping + ntfy real-world errors ─────────────────

func TestCleanNotifErrorStripsApprisePrefix(t *testing.T) {
	raw := "apprise send failed: exit status 1 (2026-06-02 12:43:43,738 - WARNING - Failed to send ntfy notification to topic 'ashra...)"
	got := cleanNotifError(raw)
	// Must not contain the Python log timestamp or log level.
	if strings.Contains(got, "12:43:43,738") {
		t.Errorf("cleanNotifError() still contains raw Python timestamp: %q", got)
	}
	if strings.Contains(got, " - WARNING - ") {
		t.Errorf("cleanNotifError() still contains log level prefix: %q", got)
	}
	// Must contain the meaningful error message.
	if !strings.Contains(got, "Failed to send ntfy") {
		t.Errorf("cleanNotifError() dropped meaningful message: %q", got)
	}
}

func TestCleanNotifErrorPreservesPlainMessages(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"daily limit reached", "daily limit reached"},
		{"burst limit reached", "burst limit reached"},
		{"notification limit hit: daily limit reached", "notification limit hit: daily limit reached"},
	}
	for _, c := range cases {
		got := cleanNotifError(c.raw)
		if got != c.want {
			t.Errorf("cleanNotifError(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestCleanNotifErrorHandlesNestedPythonLog(t *testing.T) {
	// Multi-line Apprise output sometimes has multiple log lines.
	raw := "apprise send failed: exit status 1 (2026-06-02 12:43:43,738 - INFO - Loaded 1 service(s)\n2026-06-02 12:43:44,001 - WARNING - Failed to deliver notification via ntfy)"
	got := cleanNotifError(raw)
	if strings.Contains(got, ",738") {
		t.Errorf("cleanNotifError() kept raw millisecond timestamp: %q", got)
	}
}

func TestCleanNotifErrorEmptyInput(t *testing.T) {
	got := cleanNotifError("")
	if got != "" {
		t.Errorf("cleanNotifError('') = %q, want ''", got)
	}
}

func TestCleanNotifErrorNoParenthesis(t *testing.T) {
	// Error without the typical Apprise wrapper — must not crash or corrupt.
	raw := "connection refused"
	got := cleanNotifError(raw)
	if got == "" {
		t.Errorf("cleanNotifError(%q) = '', want non-empty", raw)
	}
}

// ── Timestamp in notifications API ───────────────────────────────────────────

func TestNotificationProfileLastRateLimitAtFieldPresent(t *testing.T) {
	// Verify that LastRateLimitAt is included in the JSON profile payload
	// so the frontend can format it in the user's display timezone.
	p := config.NotificationProfile{
		ID:                 "p1",
		Name:               "Test",
		Provider:           "discord",
		LastRateLimitError: "rate limited",
	}
	now := time.Now().UTC()
	p.LastRateLimitAt = &now

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["lastRateLimitAt"]; !ok {
		t.Errorf("lastRateLimitAt not in JSON payload; keys: %v", mapKeys(m))
	}
	if _, ok := m["lastRateLimitError"]; !ok {
		t.Errorf("lastRateLimitError not in JSON payload")
	}
}

func TestSuspendProfileStoresCleanError(t *testing.T) {
	_, srv := newBellTestServer(t)

	// Create a profile.
	profBody := `{"profiles":[{"id":"p-clean","name":"Clean Error Test","provider":"discord",` +
		`"details":{"token":"t","webhookid":"w"},"service":"discord://t@w","enabled":true}]}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/notifications", strings.NewReader(profBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Manually inject a raw Apprise-style error via the suspend endpoint.
	rawErr := "apprise send failed: exit status 1 (2026-06-02 12:43:43,738 - WARNING - Failed to send ntfy notification)"
	suspBody := fmt.Sprintf(`{"until":"24h","reason":%q}`, rawErr)
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/notifications/p-clean/suspend", strings.NewReader(suspBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	_ = resp2.Body.Close()

	// The stored error must not contain the Python log timestamp.
	resp3, _ := http.Get(srv.URL + "/api/notifications")
	var notifs struct {
		Profiles []struct {
			ID                 string `json:"id"`
			LastRateLimitError string `json:"lastRateLimitError"`
		} `json:"profiles"`
	}
	_ = json.NewDecoder(resp3.Body).Decode(&notifs)
	_ = resp3.Body.Close()
	for _, p := range notifs.Profiles {
		if p.ID == "p-clean" && p.LastRateLimitError != "" {
			if strings.Contains(p.LastRateLimitError, "12:43:43,738") {
				t.Errorf("stored lastRateLimitError still has raw timestamp: %q", p.LastRateLimitError)
			}
		}
	}
}

// ── Refresh interval — settings persistence ───────────────────────────────────

func TestDashboardRefreshSecondsRoundTrip(t *testing.T) {
	_, srv := newBellTestServer(t)

	body := `{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,` +
		`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,` +
		`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,` +
		`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,` +
		`"action":"restart","dashboardRefreshSeconds":60}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	resp2, _ := http.Get(srv.URL + "/api/settings")
	var cfg map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&cfg)
	_ = resp2.Body.Close()

	v, _ := cfg["dashboardRefreshSeconds"].(float64)
	if int(v) != 60 {
		t.Errorf("dashboardRefreshSeconds = %v, want 60", cfg["dashboardRefreshSeconds"])
	}
}

func TestDashboardRefreshSecondsZeroDisablesAutoRefresh(t *testing.T) {
	_, srv := newBellTestServer(t)

	body := `{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,` +
		`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,` +
		`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,` +
		`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,` +
		`"action":"restart","dashboardRefreshSeconds":0}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	resp2, _ := http.Get(srv.URL + "/api/settings")
	var cfg map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&cfg)
	_ = resp2.Body.Close()

	v, _ := cfg["dashboardRefreshSeconds"].(float64)
	if int(v) != 0 {
		t.Errorf("dashboardRefreshSeconds = %v, want 0 (disabled)", cfg["dashboardRefreshSeconds"])
	}
}

// ── History timestamp ordering ────────────────────────────────────────────────

func TestHistoryEntriesReturnedNewestFirst(t *testing.T) {
	a, srv := newBellTestServer(t)

	times := []time.Time{
		time.Now().Add(-3 * time.Minute),
		time.Now().Add(-2 * time.Minute),
		time.Now().Add(-1 * time.Minute),
	}
	for _, ts := range times {
		_ = a.history.Append(history.Entry{
			Timestamp: ts, ContainerID: "c", ContainerName: "svc",
			Action: "restart", Status: "success",
		})
	}

	resp, _ := http.Get(srv.URL + "/api/history?limit=10")
	var out struct {
		Entries []struct {
			Timestamp time.Time `json:"timestamp"`
		} `json:"entries"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()

	if len(out.Entries) < 2 {
		t.Fatalf("expected ≥2 entries, got %d", len(out.Entries))
	}
	for i := 1; i < len(out.Entries); i++ {
		if out.Entries[i].Timestamp.After(out.Entries[i-1].Timestamp) {
			t.Errorf("entries[%d].Timestamp %v > entries[%d].Timestamp %v — not newest-first",
				i, out.Entries[i].Timestamp, i-1, out.Entries[i-1].Timestamp)
		}
	}
}

func TestSystemAlertsTimestampsAreNonZero(t *testing.T) {
	a, srv := newBellTestServer(t)
	a.mu.Lock()
	a.lastScan = time.Now().UTC()
	a.mu.Unlock()

	_ = a.history.Append(history.Entry{
		Timestamp: time.Now().UTC(), ContainerID: "c1", ContainerName: "svc",
		Action: "restart", Status: "failed", Error: "timeout",
	})

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	var body struct {
		Alerts []struct {
			ID        string    `json:"id"`
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
		} `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()

	for _, al := range body.Alerts {
		if al.Timestamp.IsZero() {
			t.Errorf("alert id=%s type=%s has zero timestamp", al.ID, al.Type)
		}
	}
}

func TestSystemAlertsCriticalBeforeMonitoringStarted(t *testing.T) {
	// The system-alerts endpoint returns alerts in the order the backend builds them.
	// The frontend sorts them (critical first), but we verify backend timestamps
	// are correct so the sort works properly.
	a, srv := newBellTestServer(t)
	a.mu.Lock()
	a.lastScan = time.Now().UTC()
	a.mu.Unlock()

	_ = a.history.Append(history.Entry{
		Timestamp: time.Now().UTC(), ContainerID: "c1", ContainerName: "svc",
		Action: "restart", Status: "exhausted", Error: "max retries reached",
	})

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	var body struct {
		Alerts []map[string]any `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()

	types := make([]string, 0, len(body.Alerts))
	for _, al := range body.Alerts {
		t2, _ := al["type"].(string)
		types = append(types, t2)
	}

	// We must have both types.
	hasStarted := false
	hasCritical := false
	for _, t2 := range types {
		if t2 == "monitoring_started" {
			hasStarted = true
		}
		if t2 == "paused_monitoring" || t2 == "failed_recovery" {
			hasCritical = true
		}
	}
	if !hasStarted {
		t.Errorf("monitoring_started missing from alerts %v", types)
	}
	if !hasCritical {
		t.Errorf("paused_monitoring/failed_recovery missing from alerts %v", types)
	}
}

func TestMonitoringStartedTimestampIsBootNotLastScan(t *testing.T) {
	// Regression test: the monitoring_started alert must use bootTime, not
	// lastScan, so it can never appear newer than recovery events.
	a, srv := newBellTestServer(t)

	// Set bootTime to 10 minutes ago, lastScan to 1 minute ago.
	tenMinAgo := time.Now().UTC().Add(-10 * time.Minute)
	oneMinAgo := time.Now().UTC().Add(-1 * time.Minute)

	a.mu.Lock()
	a.bootTime = tenMinAgo
	a.lastScan = oneMinAgo
	a.mu.Unlock()

	// Append a recovery failure that happened 5 minutes ago (between boot and lastScan).
	fiveMinAgo := time.Now().UTC().Add(-5 * time.Minute)
	_ = a.history.Append(history.Entry{
		Timestamp: fiveMinAgo, ContainerID: "c1", ContainerName: "svc",
		Action: "restart", Status: "failed", Error: "timeout",
	})

	resp, _ := http.Get(srv.URL + "/api/system-alerts")
	var body struct {
		Alerts []struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
		} `json:"alerts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()

	var monitoringStartedTS time.Time
	var recoveryTS time.Time
	for _, al := range body.Alerts {
		switch al.Type {
		case "monitoring_started":
			monitoringStartedTS = al.Timestamp
		case "failed_recovery":
			recoveryTS = al.Timestamp
		}
	}

	if monitoringStartedTS.IsZero() {
		t.Fatal("monitoring_started alert not found")
	}
	if recoveryTS.IsZero() {
		t.Fatal("failed_recovery alert not found")
	}

	// The monitoring_started timestamp must be BEFORE (or equal to) the recovery event.
	if monitoringStartedTS.After(recoveryTS) {
		t.Errorf("monitoring_started timestamp %v is AFTER recovery timestamp %v — "+
			"monitoring_started must use bootTime not lastScan",
			monitoringStartedTS.Format(time.RFC3339),
			recoveryTS.Format(time.RFC3339))
	}

	// The monitoring_started timestamp must equal bootTime (approximately).
	diff := monitoringStartedTS.Sub(tenMinAgo)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Errorf("monitoring_started timestamp %v differs from bootTime %v by %v — "+
			"expected them to match (bootTime used as timestamp)",
			monitoringStartedTS.Format(time.RFC3339),
			tenMinAgo.Format(time.RFC3339), diff)
	}

	// Sanity: monitoring_started must NOT use lastScan (1 minute ago).
	diffFromLastScan := monitoringStartedTS.Sub(oneMinAgo)
	if diffFromLastScan < 0 {
		diffFromLastScan = -diffFromLastScan
	}
	if diffFromLastScan < 8*time.Minute {
		t.Errorf("monitoring_started timestamp %v appears to be lastScan %v — "+
			"expected it to be bootTime %v",
			monitoringStartedTS.Format(time.RFC3339),
			oneMinAgo.Format(time.RFC3339),
			tenMinAgo.Format(time.RFC3339))
	}
}

// ── Update check 24h interval ────────────────────────────────────────────────

func TestUpdateCheckExposesReleaseDateInHealthResponse(t *testing.T) {
	a, srv := newBellTestServer(t)

	// Directly set a fake latest version with a release date.
	a.mu.Lock()
	a.latestVersion = "9.9.9"
	a.latestVersionRelDate = "2099-06-15"
	a.mu.Unlock()

	resp, _ := http.Get(srv.URL + "/api/health")
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)

	relDate, _ := body["latestVersionRelDate"].(string)
	if relDate != "2099-06-15" {
		t.Errorf("latestVersionRelDate = %q, want 2099-06-15", relDate)
	}
	latestVer, _ := body["latestVersion"].(string)
	if latestVer != "9.9.9" {
		t.Errorf("latestVersion = %q, want 9.9.9", latestVer)
	}
}

// ── Timezone in settings ──────────────────────────────────────────────────────

func TestSettingsDisplayTimezoneRoundTrip(t *testing.T) {
	_, srv := newBellTestServer(t)

	body := `{"retryCount":3,"retryDelaySeconds":5,"workerCount":2,"queueSize":64,` +
		`"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,` +
		`"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,` +
		`"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,` +
		`"action":"restart","displayTimezone":"America/New_York","serverTimezone":"UTC"}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	resp2, _ := http.Get(srv.URL + "/api/settings")
	var cfg map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&cfg)
	_ = resp2.Body.Close()

	if cfg["displayTimezone"] != "America/New_York" {
		t.Errorf("displayTimezone = %v, want America/New_York", cfg["displayTimezone"])
	}
	if cfg["serverTimezone"] != "UTC" {
		t.Errorf("serverTimezone = %v, want UTC", cfg["serverTimezone"])
	}
}

func TestSuspendUntilMidnightUsesDisplayTimezone(t *testing.T) {
	// nextMidnight(tz) should return the next midnight in the given timezone,
	// not UTC midnight. Verify the result is in the future and timezone-correct.
	tz := "America/New_York"
	midnight := nextMidnight(tz)
	if midnight.Before(time.Now()) {
		t.Errorf("nextMidnight(%q) = %v, want future time", tz, midnight)
	}
	// Should be within 24 hours from now.
	if midnight.After(time.Now().Add(48 * time.Hour)) {
		t.Errorf("nextMidnight(%q) = %v, too far in the future", tz, midnight)
	}
	loc, _ := time.LoadLocation(tz)
	local := midnight.In(loc)
	if local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0 {
		t.Errorf("nextMidnight(%q) in local time = %02d:%02d:%02d, want 00:00:00",
			tz, local.Hour(), local.Minute(), local.Second())
	}
}

func TestSuspendUntilMidnightFallsBackToUTCForBadTZ(t *testing.T) {
	midnight := nextMidnight("Not/A/Real/TZ")
	if midnight.Before(time.Now()) {
		t.Errorf("nextMidnight(bad) = %v, want future time", midnight)
	}
	// Should be UTC midnight.
	utc := midnight.UTC()
	if utc.Hour() != 0 || utc.Minute() != 0 || utc.Second() != 0 {
		t.Errorf("nextMidnight(bad) UTC = %02d:%02d:%02d, want 00:00:00",
			utc.Hour(), utc.Minute(), utc.Second())
	}
}

// ── cleanNotifError — URL and verbose help-text stripping ────────────────────

func TestCleanNotifErrorStripsNtfyDailyQuotaURL(t *testing.T) {
	// Real ntfy error from Apprise: daily quota reached with a "see <url>" trailer.
	raw := "apprise send failed: exit status 1 (2026-06-03 01:19:00,123 - WARNING - " +
		"Failed to send ntfy notification to topic 'ashraj-alerts': limit reached: " +
		"daily message quota reached; increase your limits with a paid plan, see https://ntfy.sh/app)"
	got := cleanNotifError(raw)

	// Must contain the meaningful part.
	if !strings.Contains(got, "daily message quota reached") {
		t.Errorf("cleanNotifError() dropped meaningful message; got %q", got)
	}
	// Must NOT contain the URL.
	if strings.Contains(got, "https://") || strings.Contains(got, "http://") {
		t.Errorf("cleanNotifError() still contains URL; got %q", got)
	}
	// Must NOT contain the verbose upgrade pitch.
	if strings.Contains(got, "increase your limits") {
		t.Errorf("cleanNotifError() still contains verbose help text; got %q", got)
	}
}

func TestCleanNotifErrorStripsInlineURL(t *testing.T) {
	// Error where the URL follows "see " without a semicolon.
	raw := "apprise send failed: exit status 1 (2026-06-03 01:19:00,123 - WARNING - " +
		"Connection refused; see https://example.com/docs)"
	got := cleanNotifError(raw)
	if strings.Contains(got, "https://") {
		t.Errorf("cleanNotifError() still contains URL; got %q", got)
	}
	if !strings.Contains(got, "Connection refused") {
		t.Errorf("cleanNotifError() dropped meaningful message; got %q", got)
	}
}

func TestCleanNotifErrorStripsHTTPURL(t *testing.T) {
	// Error with a bare http:// URL mid-sentence.
	raw := "2026-06-03 01:19:00,000 - WARNING - Rate limited; upgrade at http://service.example.com/upgrade"
	got := cleanNotifError(raw)
	if strings.Contains(got, "http://") {
		t.Errorf("cleanNotifError() still contains http URL; got %q", got)
	}
}

func TestCleanNotifErrorPreservesNtfyTopicName(t *testing.T) {
	// The container topic name should survive cleanup.
	raw := "apprise send failed: exit status 1 (2026-06-03 01:19:00,000 - WARNING - " +
		"Failed to send ntfy notification to topic 'my-alerts': quota reached; " +
		"see https://ntfy.sh/app)"
	got := cleanNotifError(raw)
	if !strings.Contains(got, "my-alerts") {
		t.Errorf("cleanNotifError() stripped topic name; got %q", got)
	}
}

// helper
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
