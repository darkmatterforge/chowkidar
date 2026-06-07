package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chowkidar/internal/config"
)

// settingsBody builds a minimal settings payload with only logLevel set,
// inheriting safe defaults for all required fields from the test app config.
func settingsBodyWithLogLevel(t *testing.T, a *app, level string) []byte {
	t.Helper()
	fc := config.ToFileConfig(a.getConfig())
	fc.LogLevel = level
	b, err := json.Marshal(fc)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// TestLogLevelChangeAppliedImmediately verifies that PUT /api/settings with a
// new logLevel updates configuredLogLevel in memory right away.
func TestLogLevelChangeAppliedImmediately(t *testing.T) {
	a, srv := newIntegrationTestServer(t)

	// Baseline — should default to INFO.
	if configuredLogLevel != levelInfo {
		t.Fatalf("baseline configuredLogLevel = %v, want levelInfo", configuredLogLevel)
	}

	body := settingsBodyWithLogLevel(t, a, "debug")
	mustHTTPStatus(t, http.MethodPut, srv.URL+"/api/settings", body, http.StatusOK)

	if configuredLogLevel != levelDebug {
		t.Errorf("after PUT logLevel=debug: configuredLogLevel = %v, want levelDebug", configuredLogLevel)
	}

	// Restore so we don't bleed into other tests.
	restore := settingsBodyWithLogLevel(t, a, "info")
	mustHTTPStatus(t, http.MethodPut, srv.URL+"/api/settings", restore, http.StatusOK)
}

// TestLogLevelReflectedInGET verifies that GET /api/settings returns the
// logLevel that was just set via PUT, not a stale env-reloaded value.
func TestLogLevelReflectedInGET(t *testing.T) {
	a, srv := newIntegrationTestServer(t)

	for _, level := range []string{"debug", "warn", "error", "info"} {
		body := settingsBodyWithLogLevel(t, a, level)
		mustHTTPStatus(t, http.MethodPut, srv.URL+"/api/settings", body, http.StatusOK)

		resp := mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/settings", nil, http.StatusOK)
		var got map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode GET response: %v", err)
		}
		resp.Body.Close()

		if got["logLevel"] != level {
			t.Errorf("GET logLevel = %v, want %q", got["logLevel"], level)
		}
	}
}

// TestLogLevelWithEnvOverride verifies that when LOG_LEVEL env var is set,
// a UI-requested change still takes immediate effect (live-until-restart),
// and GET /api/settings reflects the live level (not the env value).
func TestLogLevelWithEnvOverride(t *testing.T) {
	t.Setenv("LOG_LEVEL", "error") // simulate env var being set to a specific level
	a, srv := newIntegrationTestServer(t)

	body := settingsBodyWithLogLevel(t, a, "debug")
	mustHTTPStatus(t, http.MethodPut, srv.URL+"/api/settings", body, http.StatusOK)

	// configuredLogLevel must be debug (user's choice), not error (env value).
	if configuredLogLevel != levelDebug {
		t.Errorf("configuredLogLevel = %v, want levelDebug (env override should not suppress live change)", configuredLogLevel)
	}

	// GET /api/settings must return "debug", not "error".
	resp := mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/settings", nil, http.StatusOK)
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	resp.Body.Close()

	if got["logLevel"] != "debug" {
		t.Errorf("GET logLevel = %v, want \"debug\" (live value, not env value)", got["logLevel"])
	}

	// envOverrides must still advertise the env value so the UI can show its note.
	overrides, _ := got["envOverrides"].(map[string]any)
	if overrides["logLevel"] != "error" {
		t.Errorf("GET envOverrides.logLevel = %v, want \"error\"", overrides["logLevel"])
	}
}

// TestShouldLog verifies that shouldLog correctly gates messages by level.
func TestShouldLog(t *testing.T) {
	cases := []struct {
		configured logLevel
		tested     logLevel
		want       bool
	}{
		{levelDebug, levelDebug, true},
		{levelDebug, levelInfo, true},
		{levelDebug, levelWarn, true},
		{levelDebug, levelError, true},
		{levelInfo, levelDebug, false},
		{levelInfo, levelInfo, true},
		{levelInfo, levelWarn, true},
		{levelWarn, levelDebug, false},
		{levelWarn, levelInfo, false},
		{levelWarn, levelWarn, true},
		{levelWarn, levelError, true},
		{levelError, levelDebug, false},
		{levelError, levelInfo, false},
		{levelError, levelWarn, false},
		{levelError, levelError, true},
	}
	for _, tc := range cases {
		configuredLogLevel = tc.configured
		got := shouldLog(tc.tested)
		if got != tc.want {
			t.Errorf("shouldLog(tested=%v, configured=%v) = %v, want %v",
				tc.tested, tc.configured, got, tc.want)
		}
	}
	configuredLogLevel = levelInfo // restore
}

// TestLogAtDoesNotPanicBelowThreshold ensures logAt is silent (no output)
// when the message level is below the configured threshold.
func TestLogAtDoesNotPanicBelowThreshold(t *testing.T) {
	configuredLogLevel = levelError
	defer func() { configuredLogLevel = levelInfo }()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(log.Writer()) // restore previous writer

	logDebugf("should not appear")
	logInfof("should not appear")
	logWarnf("should not appear")

	if buf.Len() > 0 {
		t.Errorf("expected no log output below ERROR threshold, got: %s", buf.String())
	}

	logErrorf("should appear")
	if buf.Len() == 0 {
		t.Error("expected log output at ERROR level, got nothing")
	}
}

// TestLogFileWritesDebugAtDebugLevel verifies that when logToFile is enabled
// and the level is DEBUG, debug messages are written to the rotating log file.
func TestLogFileWritesDebugAtDebugLevel(t *testing.T) {
	logsDir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	applyLogConfig(logsDir, config.Config{LogLevel: "debug", LogToFile: true})
	t.Cleanup(func() {
		applyLogConfig("", config.Config{LogLevel: "info", LogToFile: false})
	})

	logDebugf("test-debug-marker from unit test")
	logInfof("test-info-marker from unit test")

	// Flush by closing the writer before reading.
	if globalLogWriter != nil {
		globalLogWriter.mu.Lock()
		if globalLogWriter.file != nil {
			_ = globalLogWriter.file.Sync()
		}
		globalLogWriter.mu.Unlock()
	}

	logFile := filepath.Join(logsDir, "app-"+time.Now().Format("2006-01-02")+".log")
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file %s: %v", logFile, err)
	}
	got := string(content)

	if !strings.Contains(got, "[DEBUG]") {
		t.Errorf("log file missing [DEBUG] entry; got:\n%s", got)
	}
	if !strings.Contains(got, "test-debug-marker") {
		t.Errorf("log file missing debug message text; got:\n%s", got)
	}
	if !strings.Contains(got, "[INFO]") {
		t.Errorf("log file missing [INFO] entry; got:\n%s", got)
	}
}

// TestLogFileExcludesBelowThreshold verifies that messages below the configured
// level are NOT written to the log file.
func TestLogFileExcludesBelowThreshold(t *testing.T) {
	logsDir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	applyLogConfig(logsDir, config.Config{LogLevel: "warn", LogToFile: true})
	t.Cleanup(func() {
		applyLogConfig("", config.Config{LogLevel: "info", LogToFile: false})
	})

	logDebugf("should-not-appear-debug")
	logInfof("should-not-appear-info")
	logWarnf("should-appear-warn")

	if globalLogWriter != nil {
		globalLogWriter.mu.Lock()
		if globalLogWriter.file != nil {
			_ = globalLogWriter.file.Sync()
		}
		globalLogWriter.mu.Unlock()
	}

	logFile := filepath.Join(logsDir, "app-"+time.Now().Format("2006-01-02")+".log")
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file %s: %v", logFile, err)
	}
	got := string(content)

	if strings.Contains(got, "[DEBUG]") {
		t.Errorf("log file should not contain [DEBUG] at warn level; got:\n%s", got)
	}
	if strings.Contains(got, "[INFO]") {
		t.Errorf("log file should not contain [INFO] at warn level; got:\n%s", got)
	}
	if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "should-appear-warn") {
		t.Errorf("log file missing expected [WARN] entry; got:\n%s", got)
	}
}
