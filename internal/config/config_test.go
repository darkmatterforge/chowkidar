package config

import (
	"reflect"
	"testing"
)

// TestLoadFreshInstall verifies that Load() produces correct defaults when
// there is no config.yaml on disk and no env vars are set.
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
		{"NotificationCooldownSeconds", cfg.NotificationCooldownSeconds, d.NotificationCooldownSeconds},
		{"LogLevel", cfg.LogLevel, d.LogLevel},
		{"LogToFile", cfg.LogToFile, d.LogToFile},
		{"LogRetentionDays", cfg.LogRetentionDays, d.LogRetentionDays},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestLoadEnvVarOverrides verifies that env vars take precedence over defaults.
func TestLoadEnvVarOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_PATH", dir)
	t.Setenv("WORKER_COUNT", "5")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("EXTERNAL_HOSTNAME", "my-unraid")
	t.Setenv("ACTION", "stop")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.WorkerCount != 5 {
		t.Errorf("WorkerCount = %d, want 5", cfg.WorkerCount)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"warn\"", cfg.LogLevel)
	}
	if cfg.ExternalHostname != "my-unraid" {
		t.Errorf("ExternalHostname = %q, want \"my-unraid\"", cfg.ExternalHostname)
	}
	if cfg.Action != "stop" {
		t.Errorf("Action = %q, want \"stop\"", cfg.Action)
	}
}

// TestLoadFileConfigOverrides verifies that config.yaml values override defaults.
func TestLoadFileConfigOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_PATH", dir)

	file := defaultFileConfig()
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

// TestLoadPriorityChain verifies env var > config.yaml > application default.
func TestLoadPriorityChain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_PATH", dir)

	file := defaultFileConfig()
	file.WorkerCount = 4
	file.LogLevel = "warn"
	if err := SaveFileConfig(dir, file); err != nil {
		t.Fatalf("SaveFileConfig() error = %v", err)
	}

	t.Setenv("WORKER_COUNT", "11")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.WorkerCount != 11 {
		t.Errorf("WorkerCount = %d, want 11 (env var should win over file)", cfg.WorkerCount)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"warn\" (file should win over default)", cfg.LogLevel)
	}
	if cfg.QueueSize != 64 {
		t.Errorf("QueueSize = %d, want 64 (application default)", cfg.QueueSize)
	}
}

func TestFileConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := defaultFileConfig()
	orig.Port = "9090"
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
	orig.StartExited = true
	orig.RunScriptPath = "/config/scripts/heal.sh"
	orig.ActionTimeoutSeconds = 35
	orig.RequireFilterForExited = true

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
		StartExited:                   true,
		RunScriptPath:                 "/config/scripts/heal.sh",
		ActionTimeoutSeconds:          31,
		RequireFilterForExited:        false,
	}

	got := ToFileConfig(cfg)
	if got.HttpMaxIdleConns != 22 || got.HttpMaxIdleConnsPerHost != 11 || got.HttpIdleConnTimeoutSeconds != 88 {
		t.Fatalf("HTTP pool fields not preserved: %#v", got)
	}
	if got.DockerClientRetryCount != 2 || got.DockerClientRetryDelaySeconds != 5 {
		t.Fatalf("Docker retry fields not preserved: %#v", got)
	}
}
