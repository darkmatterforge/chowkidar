package config

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestLoadFreshInstall verifies that Load() produces correct defaults when
// there is no config.yaml on disk and no env vars are set. This simulates
// a first-ever boot on a fresh installation.
func TestLoadFreshInstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_PATH", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	d := defaultFileConfig()

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, d.Port},
		{"ConfigDir", cfg.ConfigDir, dir},
		{"Action", cfg.Action, d.Action},
		{"RetryCount", cfg.RetryCount, d.RetryCount},
		{"RetryDelay", cfg.RetryDelay, time.Duration(d.RetryDelaySeconds) * time.Second},
		{"WorkerCount", cfg.WorkerCount, d.WorkerCount},
		{"QueueSize", cfg.QueueSize, d.QueueSize},
		{"ActionTimeoutSeconds", cfg.ActionTimeoutSeconds, d.ActionTimeoutSeconds},
		{"RequireFilterForExited", cfg.RequireFilterForExited, d.RequireFilterForExited},
		{"StartExited", cfg.StartExited, d.StartExited},
		{"DockerSocketPath", cfg.DockerSocketPath, d.DockerSocketPath},
		{"DockerPingTimeoutSeconds", cfg.DockerPingTimeoutSeconds, d.DockerPingTimeoutSeconds},
		{"DockerClientRetryCount", cfg.DockerClientRetryCount, d.DockerClientRetryCount},
		{"DockerClientRetryDelaySeconds", cfg.DockerClientRetryDelaySeconds, d.DockerClientRetryDelaySeconds},
		{"HttpClientTimeoutSeconds", cfg.HttpClientTimeoutSeconds, d.HttpClientTimeoutSeconds},
		{"HttpMaxIdleConns", cfg.HttpMaxIdleConns, d.HttpMaxIdleConns},
		{"HttpMaxIdleConnsPerHost", cfg.HttpMaxIdleConnsPerHost, d.HttpMaxIdleConnsPerHost},
		{"HttpIdleConnTimeoutSeconds", cfg.HttpIdleConnTimeoutSeconds, d.HttpIdleConnTimeoutSeconds},
		{"NotificationRatePerSec", cfg.NotificationRatePerSec, d.NotificationRatePerSec},
		{"NotificationCooldownSeconds", cfg.NotificationCooldownSeconds, d.NotificationCooldownSeconds},
		{"LogLevel", cfg.LogLevel, d.LogLevel},
		{"LogToFile", cfg.LogToFile, d.LogToFile},
		{"LogRetentionDays", cfg.LogRetentionDays, d.LogRetentionDays},
		{"SQLiteBusyTimeoutSeconds", cfg.SQLiteBusyTimeoutSeconds, d.SQLiteBusyTimeoutSeconds},
		{"SQLitePragmas", cfg.SQLitePragmas, d.SQLitePragmas},
		// SQLitePath must be derived from APP_PATH, not hardcoded.
		{"SQLitePath", cfg.SQLitePath, filepath.Join(dir, "data", "app.db")},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestLoadEnvVarOverrides verifies that env vars take precedence over the
// application defaults when no config.yaml exists.
func TestLoadEnvVarOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_PATH", dir)
	t.Setenv("RETRY_COUNT", "7")
	t.Setenv("WORKER_COUNT", "5")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("SQLITE_PATH", "/tmp/custom.db")
	t.Setenv("EXTERNAL_HOSTNAME", "my-unraid")
	t.Setenv("ACTION", "stop")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.RetryCount != 7 {
		t.Errorf("RetryCount = %d, want 7", cfg.RetryCount)
	}
	if cfg.WorkerCount != 5 {
		t.Errorf("WorkerCount = %d, want 5", cfg.WorkerCount)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"warn\"", cfg.LogLevel)
	}
	if cfg.SQLitePath != "/tmp/custom.db" {
		t.Errorf("SQLitePath = %q, want \"/tmp/custom.db\"", cfg.SQLitePath)
	}
	if cfg.ExternalHostname != "my-unraid" {
		t.Errorf("ExternalHostname = %q, want \"my-unraid\"", cfg.ExternalHostname)
	}
	if cfg.Action != "stop" {
		t.Errorf("Action = %q, want \"stop\"", cfg.Action)
	}
}

// TestLoadFileConfigOverrides verifies that values written to config.yaml by
// the user (or UI) override application defaults when no env vars are set.
func TestLoadFileConfigOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_PATH", dir)

	file := defaultFileConfig()
	file.RetryCount = 9
	file.Action = "none"
	file.LogLevel = "error"
	file.WorkerCount = 4
	file.ExternalHostname = "nas-box"
	if err := SaveFileConfig(dir, file); err != nil {
		t.Fatalf("SaveFileConfig() error = %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.RetryCount != 9 {
		t.Errorf("RetryCount = %d, want 9", cfg.RetryCount)
	}
	if cfg.Action != "none" {
		t.Errorf("Action = %q, want \"none\"", cfg.Action)
	}
	if cfg.LogLevel != "error" {
		t.Errorf("LogLevel = %q, want \"error\"", cfg.LogLevel)
	}
	if cfg.WorkerCount != 4 {
		t.Errorf("WorkerCount = %d, want 4", cfg.WorkerCount)
	}
	if cfg.ExternalHostname != "nas-box" {
		t.Errorf("ExternalHostname = %q, want \"nas-box\"", cfg.ExternalHostname)
	}
}

// TestLoadPriorityChain verifies the full override hierarchy:
// env var > config.yaml > application default.
func TestLoadPriorityChain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_PATH", dir)

	// Write config.yaml with a non-default value.
	file := defaultFileConfig()
	file.RetryCount = 5        // override default (3)
	file.WorkerCount = 4       // override default (2)
	file.LogLevel = "warn"     // override default ("info")
	if err := SaveFileConfig(dir, file); err != nil {
		t.Fatalf("SaveFileConfig() error = %v", err)
	}

	// Env var overrides the file value for RetryCount only.
	t.Setenv("RETRY_COUNT", "11")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Env var wins over file.
	if cfg.RetryCount != 11 {
		t.Errorf("RetryCount = %d, want 11 (env var should win over file)", cfg.RetryCount)
	}
	// File wins over default.
	if cfg.WorkerCount != 4 {
		t.Errorf("WorkerCount = %d, want 4 (file should win over default)", cfg.WorkerCount)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"warn\" (file should win over default)", cfg.LogLevel)
	}
	// Untouched value keeps the application default.
	if cfg.QueueSize != 64 {
		t.Errorf("QueueSize = %d, want 64 (application default)", cfg.QueueSize)
	}
}

func TestFileConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := defaultFileConfig()
	orig.Port = "9090"
	orig.RetryCount = 6
	orig.RetryDelaySeconds = 12
	orig.WorkerCount = 5
	orig.QueueSize = 300
	orig.HttpClientTimeoutSeconds = 21
	orig.HttpMaxIdleConns = 25
	orig.HttpMaxIdleConnsPerHost = 15
	orig.HttpIdleConnTimeoutSeconds = 95
	orig.DockerPingTimeoutSeconds = 8
	orig.DockerClientRetryCount = 2
	orig.DockerClientRetryDelaySeconds = 4
	orig.DockerSocketPath = "/var/run/docker.sock"
	orig.ExternalHostname = "pool.example"
	orig.NotificationRatePerSec = 9
	orig.StartExited = true
	orig.RunScriptPath = "/config/scripts/heal.sh"
	orig.ActionTimeoutSeconds = 35
	orig.RequireFilterForExited = true
	orig.SQLitePath = "/config/data/app.db"
	orig.SQLiteBusyTimeoutSeconds = 6000
	orig.SQLitePragmas = "journal_mode=WAL;synchronous=NORMAL"

	if err := SaveFileConfig(dir, orig); err != nil {
		t.Fatalf("SaveFileConfig() error = %v", err)
	}

	loaded, err := LoadFileConfig(dir)
	if err != nil {
		t.Fatalf("LoadFileConfig() error = %v", err)
	}

	if !reflect.DeepEqual(orig, loaded) {
		t.Fatalf("round-trip mismatch:\norig = %#v\nloaded = %#v", orig, loaded)
	}
}

func TestToFileConfigIncludesNewFields(t *testing.T) {
	cfg := Config{
		Port:                          "8081",
		RetryCount:                    4,
		RetryDelay:                    7,
		Action:                        "restart",
		WorkerCount:                   3,
		QueueSize:                     128,
		HttpClientTimeoutSeconds:      19,
		HttpMaxIdleConns:              22,
		HttpMaxIdleConnsPerHost:       11,
		HttpIdleConnTimeoutSeconds:    88,
		DockerPingTimeoutSeconds:      9,
		DockerClientRetryCount:        2,
		DockerClientRetryDelaySeconds: 5,
		DockerSocketPath:              "/var/run/docker.sock",
		ExternalHostname:              "example.internal",
		NotificationRatePerSec:        6,
		StartExited:                   true,
		RunScriptPath:                 "/config/scripts/heal.sh",
		ActionTimeoutSeconds:          31,
		RequireFilterForExited:        false,
		SQLitePath:                    "/config/data/app.db",
		SQLiteBusyTimeoutSeconds:      5001,
		SQLitePragmas:                 "journal_mode=WAL",
	}

	got := ToFileConfig(cfg)
	if got.HttpMaxIdleConns != 22 || got.HttpMaxIdleConnsPerHost != 11 || got.HttpIdleConnTimeoutSeconds != 88 {
		t.Fatalf("HTTP pool fields not preserved: %#v", got)
	}
	if got.DockerClientRetryCount != 2 || got.DockerClientRetryDelaySeconds != 5 {
		t.Fatalf("Docker retry fields not preserved: %#v", got)
	}
	if got.SQLitePath != "/config/data/app.db" || got.SQLiteBusyTimeoutSeconds != 5001 || got.SQLitePragmas != "journal_mode=WAL" {
		t.Fatalf("SQLite fields not preserved: %#v", got)
	}
}
