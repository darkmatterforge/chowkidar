package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the resolved runtime configuration, combining values from
// config.yaml and environment-variable overrides. Created by Load().
type Config struct {
	Port                          string
	ConfigDir                     string
	RetryCount                    int
	RetryDelay                    time.Duration
	Action                        string
	AppriseServices               string
	ContainerNameFilter           string
	ContainerLabelFilter          string
	ContainerEnvVarFilter         string
	WorkerCount                   int
	QueueSize                     int
	HttpClientTimeoutSeconds      int
	HttpMaxIdleConns              int
	HttpMaxIdleConnsPerHost       int
	HttpIdleConnTimeoutSeconds    int
	DockerPingTimeoutSeconds      int
	DockerClientRetryCount        int
	DockerClientRetryDelaySeconds int
	DockerSocketPath              string
	ExternalHostname              string
	NotificationRatePerSec        int
	NotificationCooldownSeconds   int
	StartupDelaySeconds           int
	StartExited                   bool
	RunScriptPath                 string
	BootNotification              bool
	BootNotificationProfiles      []string
	ActionTimeoutSeconds          int
	RequireFilterForExited        bool
	SQLitePath                    string
	SQLiteBusyTimeoutSeconds      int
	SQLitePragmas                 string
	DisplayTimezone               string
	ServerTimezone                string
	PrimaryBaseURL                string
	LogLevel                      string
	LogToFile                     bool
	LogRetentionDays              int
	DashboardLayout               string
	DashboardRefreshSeconds       int
	Theme                         string
}

// FileConfig is the schema for config.yaml and is also the JSON shape
// returned/accepted by the /api/settings endpoint.
type FileConfig struct {
	Port                          string   `yaml:"port,omitempty"                json:"port"`
	RetryCount                    int      `yaml:"retryCount"                    json:"retryCount"`
	RetryDelaySeconds             int      `yaml:"retryDelaySeconds"             json:"retryDelaySeconds"`
	Action                        string   `yaml:"action"                        json:"action"`
	ContainerNameFilter           string   `yaml:"containerNameFilter,omitempty" json:"containerNameFilter"`
	ContainerLabelFilter          string   `yaml:"containerLabelFilter,omitempty" json:"containerLabelFilter"`
	ContainerEnvVarFilter         string   `yaml:"containerEnvVarFilter,omitempty" json:"containerEnvVarFilter"`
	WorkerCount                   int      `yaml:"workerCount"                   json:"workerCount"`
	QueueSize                     int      `yaml:"queueSize"                     json:"queueSize"`
	HttpClientTimeoutSeconds      int      `yaml:"httpClientTimeoutSeconds"      json:"httpClientTimeoutSeconds"`
	HttpMaxIdleConns              int      `yaml:"httpMaxIdleConns"              json:"httpMaxIdleConns"`
	HttpMaxIdleConnsPerHost       int      `yaml:"httpMaxIdleConnsPerHost"       json:"httpMaxIdleConnsPerHost"`
	HttpIdleConnTimeoutSeconds    int      `yaml:"httpIdleConnTimeoutSeconds"    json:"httpIdleConnTimeoutSeconds"`
	DockerPingTimeoutSeconds      int      `yaml:"dockerPingTimeoutSeconds"      json:"dockerPingTimeoutSeconds"`
	DockerClientRetryCount        int      `yaml:"dockerClientRetryCount"        json:"dockerClientRetryCount"`
	DockerClientRetryDelaySeconds int      `yaml:"dockerClientRetryDelaySeconds" json:"dockerClientRetryDelaySeconds"`
	DockerSocketPath              string   `yaml:"dockerSocketPath"              json:"dockerSocketPath"`
	ExternalHostname              string   `yaml:"externalHostname"              json:"externalHostname"`
	NotificationRatePerSec        int      `yaml:"notificationRatePerSec"        json:"notificationRatePerSec"`
	NotificationCooldownSeconds   int      `yaml:"notificationCooldownSeconds"   json:"notificationCooldownSeconds"`
	StartupDelaySeconds           int      `yaml:"startupDelaySeconds"           json:"startupDelaySeconds"`
	StartExited                   bool     `yaml:"startExited"                   json:"startExited"`
	RunScriptPath                 string   `yaml:"runScriptPath"                 json:"runScriptPath"`
	BootNotification              bool     `yaml:"bootNotification"              json:"bootNotification"`
	BootNotificationProfiles      []string `yaml:"bootNotificationProfiles,omitempty" json:"bootNotificationProfiles,omitempty"`
	ActionTimeoutSeconds          int      `yaml:"actionTimeoutSeconds"          json:"actionTimeoutSeconds"`
	RequireFilterForExited        bool     `yaml:"requireFilterForExited"        json:"requireFilterForExited"`
	SQLitePath                    string   `yaml:"sqlitePath"                    json:"sqlitePath"`
	SQLiteBusyTimeoutSeconds      int      `yaml:"sqliteBusyTimeoutSeconds"      json:"sqliteBusyTimeoutSeconds"`
	SQLitePragmas                 string   `yaml:"sqlitePragmas"                 json:"sqlitePragmas"`
	DisplayTimezone               string   `yaml:"displayTimezone"               json:"displayTimezone"`
	ServerTimezone                string   `yaml:"serverTimezone"                json:"serverTimezone"`
	PrimaryBaseURL                string   `yaml:"primaryBaseURL"                json:"primaryBaseURL"`
	LogLevel                      string   `yaml:"logLevel"                      json:"logLevel"`
	LogToFile                     bool     `yaml:"logToFile"                     json:"logToFile"`
	LogRetentionDays              int      `yaml:"logRetentionDays"              json:"logRetentionDays"`
	DashboardLayout               string   `yaml:"dashboardLayout"               json:"dashboardLayout"`
	DashboardRefreshSeconds       int      `yaml:"dashboardRefreshSeconds"       json:"dashboardRefreshSeconds"`
	Theme                         string   `yaml:"theme"                         json:"theme"`
}

// Load reads config.yaml from the directory pointed to by APP_PATH (default "/config")
// and overlays environment-variable overrides, returning the resolved Config.
func Load() (Config, error) {
	configDir := getEnv("APP_PATH", "/config")
	fileCfg, err := LoadFileConfig(configDir)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Port:                          envOr("APP_PORT", fileCfg.Port),
		ConfigDir:                     configDir,
		Action:                        strings.ToLower(envOr("ACTION", fileCfg.Action)),
		ContainerNameFilter:           envOr("CONTAINER_NAME", fileCfg.ContainerNameFilter),
		ContainerLabelFilter:          envOr("CONTAINER_LABEL", fileCfg.ContainerLabelFilter),
		ContainerEnvVarFilter:         envOr("CONTAINER_ENV_VAR", fileCfg.ContainerEnvVarFilter),
		DockerSocketPath:              envOr("DOCKER_SOCKET_PATH", fileCfg.DockerSocketPath),
		ExternalHostname:              envOr("EXTERNAL_HOSTNAME", fileCfg.ExternalHostname),
		NotificationRatePerSec:        envOrInt("NOTIFICATION_RATE_PER_SEC", fileCfg.NotificationRatePerSec),
		NotificationCooldownSeconds:   envOrInt("NOTIFICATION_COOLDOWN_SECONDS", fileCfg.NotificationCooldownSeconds),
		StartupDelaySeconds:           envOrInt("STARTUP_DELAY_SECONDS", fileCfg.StartupDelaySeconds),
		HttpClientTimeoutSeconds:      envOrInt("HTTP_CLIENT_TIMEOUT_SECONDS", fileCfg.HttpClientTimeoutSeconds),
		HttpMaxIdleConns:              envOrInt("HTTP_MAX_IDLE_CONNS", fileCfg.HttpMaxIdleConns),
		HttpMaxIdleConnsPerHost:       envOrInt("HTTP_MAX_IDLE_CONNS_PER_HOST", fileCfg.HttpMaxIdleConnsPerHost),
		HttpIdleConnTimeoutSeconds:    envOrInt("HTTP_IDLE_CONN_TIMEOUT_SECONDS", fileCfg.HttpIdleConnTimeoutSeconds),
		DockerPingTimeoutSeconds:      envOrInt("DOCKER_PING_TIMEOUT_SECONDS", fileCfg.DockerPingTimeoutSeconds),
		DockerClientRetryCount:        envOrInt("DOCKER_CLIENT_RETRY_COUNT", fileCfg.DockerClientRetryCount),
		DockerClientRetryDelaySeconds: envOrInt("DOCKER_CLIENT_RETRY_DELAY_SECONDS", fileCfg.DockerClientRetryDelaySeconds),
		StartExited:                   envOrBool("START_EXITED", fileCfg.StartExited),
		RunScriptPath:                 envOr("RUN_SCRIPT_PATH", fileCfg.RunScriptPath),
		BootNotification:              fileCfg.BootNotification,
		BootNotificationProfiles:      fileCfg.BootNotificationProfiles,
		ActionTimeoutSeconds:          envOrInt("ACTION_TIMEOUT_SECONDS", fileCfg.ActionTimeoutSeconds),
		RequireFilterForExited:        envOrBool("REQUIRE_FILTER_FOR_EXITED", fileCfg.RequireFilterForExited),
		SQLitePath:                    envOr("SQLITE_PATH", fileCfg.SQLitePath),
		SQLiteBusyTimeoutSeconds:      envOrInt("SQLITE_BUSY_TIMEOUT_SECONDS", fileCfg.SQLiteBusyTimeoutSeconds),
		SQLitePragmas:                 envOr("SQLITE_PRAGMAS", fileCfg.SQLitePragmas),
		DisplayTimezone:               envOr("DISPLAY_TIMEZONE", fileCfg.DisplayTimezone),
		ServerTimezone:                envOr("SERVER_TIMEZONE", fileCfg.ServerTimezone),
		PrimaryBaseURL:                envOr("PRIMARY_BASE_URL", fileCfg.PrimaryBaseURL),
		LogLevel:                      envOr("LOG_LEVEL", fileCfg.LogLevel),
		LogToFile:                     envOrBool("LOG_TO_FILE", fileCfg.LogToFile),
		LogRetentionDays:              envOrInt("LOG_RETENTION_DAYS", fileCfg.LogRetentionDays),
		DashboardLayout:               fileCfg.DashboardLayout,
		DashboardRefreshSeconds:       fileCfg.DashboardRefreshSeconds,
		Theme:                         fileCfg.Theme,
	}

	if cfg.SQLitePath == "" {
		cfg.SQLitePath = filepath.Join(configDir, "data", "app.db")
	}

	retryDelaySeconds := envOrInt("RETRY_DELAY", fileCfg.RetryDelaySeconds)
	cfg.RetryDelay = time.Duration(retryDelaySeconds) * time.Second

	cfg.RetryCount = envOrInt("RETRY_COUNT", fileCfg.RetryCount)
	cfg.WorkerCount = envOrInt("WORKER_COUNT", fileCfg.WorkerCount)
	cfg.QueueSize = envOrInt("QUEUE_SIZE", fileCfg.QueueSize)

	if cfg.WorkerCount < 1 {
		return Config{}, fmt.Errorf("WORKER_COUNT must be >= 1")
	}
	if cfg.QueueSize < 1 {
		return Config{}, fmt.Errorf("QUEUE_SIZE must be >= 1")
	}
	applyConfigDefaults(&cfg)
	return cfg, nil
}

func applyConfigDefaults(cfg *Config) {
	if cfg.ActionTimeoutSeconds < 1 {
		cfg.ActionTimeoutSeconds = 20
	}
	if cfg.HttpClientTimeoutSeconds < 1 {
		cfg.HttpClientTimeoutSeconds = 15
	}
	if cfg.HttpMaxIdleConns < 1 {
		cfg.HttpMaxIdleConns = 20
	}
	if cfg.HttpMaxIdleConnsPerHost < 1 {
		cfg.HttpMaxIdleConnsPerHost = 10
	}
	if cfg.HttpIdleConnTimeoutSeconds < 1 {
		cfg.HttpIdleConnTimeoutSeconds = 90
	}
	if cfg.DockerPingTimeoutSeconds < 1 {
		cfg.DockerPingTimeoutSeconds = 5
	}
	if cfg.DockerClientRetryCount < 1 {
		cfg.DockerClientRetryCount = 1
	}
	if cfg.DockerClientRetryDelaySeconds < 1 {
		cfg.DockerClientRetryDelaySeconds = 2
	}
	if cfg.SQLiteBusyTimeoutSeconds < 1 {
		cfg.SQLiteBusyTimeoutSeconds = 5000
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.Action == "" {
		cfg.Action = "restart"
	}
	if cfg.DockerSocketPath == "" {
		cfg.DockerSocketPath = "/var/run/docker.sock"
	}
	if cfg.NotificationRatePerSec < 1 {
		cfg.NotificationRatePerSec = 5
	}
	if cfg.RetryCount < 1 {
		cfg.RetryCount = 3
	}
	if cfg.RetryDelay < 1 {
		cfg.RetryDelay = 10 * time.Second
	}
}

// LoadFileConfig reads config.yaml from configDir, merging over built-in defaults.
// Returns defaults when the file does not exist yet.
func LoadFileConfig(configDir string) (FileConfig, error) {
	defaults := defaultFileConfig()
	filePath := filepath.Join(configDir, "config.yaml")
	raw, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return defaults, nil
		}
		return FileConfig{}, fmt.Errorf("read config file: %w", err)
	}

	merged := defaults
	if err := yaml.Unmarshal(raw, &merged); err != nil {
		return FileConfig{}, fmt.Errorf("parse config yaml: %w", err)
	}
	return merged, nil
}

// SaveFileConfig marshals fileCfg to YAML and writes it to configDir/config.yaml.
func SaveFileConfig(configDir string, fileCfg FileConfig) error {
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	filePath := filepath.Join(configDir, "config.yaml")
	data, err := yaml.Marshal(fileCfg)
	if err != nil {
		return fmt.Errorf("marshal config yaml: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return fmt.Errorf("write config yaml: %w", err)
	}
	return nil
}

// ToFileConfig converts the runtime Config back to a persistable FileConfig.
// Fields that exist only in FileConfig (e.g. DashboardRefreshSeconds) must be
// handled separately — prefer LoadFileConfig + patch over this function when
// saving from a handler to avoid silently zeroing those fields.
func ToFileConfig(cfg Config) FileConfig {
	return FileConfig{
		Port:                          cfg.Port,
		RetryCount:                    cfg.RetryCount,
		RetryDelaySeconds:             int(cfg.RetryDelay / time.Second),
		Action:                        cfg.Action,
		ContainerNameFilter:           cfg.ContainerNameFilter,
		ContainerLabelFilter:          cfg.ContainerLabelFilter,
		ContainerEnvVarFilter:         cfg.ContainerEnvVarFilter,
		WorkerCount:                   cfg.WorkerCount,
		QueueSize:                     cfg.QueueSize,
		HttpClientTimeoutSeconds:      cfg.HttpClientTimeoutSeconds,
		HttpMaxIdleConns:              cfg.HttpMaxIdleConns,
		HttpMaxIdleConnsPerHost:       cfg.HttpMaxIdleConnsPerHost,
		HttpIdleConnTimeoutSeconds:    cfg.HttpIdleConnTimeoutSeconds,
		DockerPingTimeoutSeconds:      cfg.DockerPingTimeoutSeconds,
		DockerClientRetryCount:        cfg.DockerClientRetryCount,
		DockerClientRetryDelaySeconds: cfg.DockerClientRetryDelaySeconds,
		DockerSocketPath:              cfg.DockerSocketPath,
		ExternalHostname:              cfg.ExternalHostname,
		NotificationRatePerSec:        cfg.NotificationRatePerSec,
		NotificationCooldownSeconds:   cfg.NotificationCooldownSeconds,
		StartupDelaySeconds:           cfg.StartupDelaySeconds,
		StartExited:                   cfg.StartExited,
		RunScriptPath:                 cfg.RunScriptPath,
		BootNotification:              cfg.BootNotification,
		BootNotificationProfiles:      cfg.BootNotificationProfiles,
		ActionTimeoutSeconds:          cfg.ActionTimeoutSeconds,
		RequireFilterForExited:        cfg.RequireFilterForExited,
		SQLitePath:                    cfg.SQLitePath,
		SQLiteBusyTimeoutSeconds:      cfg.SQLiteBusyTimeoutSeconds,
		SQLitePragmas:                 cfg.SQLitePragmas,
		DisplayTimezone:               cfg.DisplayTimezone,
		ServerTimezone:                cfg.ServerTimezone,
		PrimaryBaseURL:                cfg.PrimaryBaseURL,
		LogLevel:                      cfg.LogLevel,
		LogToFile:                     cfg.LogToFile,
		LogRetentionDays:              cfg.LogRetentionDays,
		DashboardLayout:               cfg.DashboardLayout,
		DashboardRefreshSeconds:       cfg.DashboardRefreshSeconds,
		Theme:                         cfg.Theme,
	}
}

func defaultFileConfig() FileConfig {
	return FileConfig{
		Port:                          "8080",
		RetryCount:                    3,
		RetryDelaySeconds:             10,
		Action:                        "restart",
		ContainerNameFilter:           "",
		ContainerLabelFilter:          "",
		ContainerEnvVarFilter:         "",
		WorkerCount:                   2,
		QueueSize:                     64,
		HttpClientTimeoutSeconds:      15,
		HttpMaxIdleConns:              20,
		HttpMaxIdleConnsPerHost:       10,
		HttpIdleConnTimeoutSeconds:    90,
		DockerPingTimeoutSeconds:      5,
		DockerClientRetryCount:        1,
		DockerClientRetryDelaySeconds: 2,
		DockerSocketPath:              "/var/run/docker.sock",
		ExternalHostname:              "",
		NotificationRatePerSec:        5,
		NotificationCooldownSeconds:   3600,
		StartExited:                   false,
		RunScriptPath:                 "",
		ActionTimeoutSeconds:          20,
		RequireFilterForExited:        true,
		SQLitePath:                    "",
		SQLiteBusyTimeoutSeconds:      5000,
		SQLitePragmas:                 "journal_mode=WAL;synchronous=NORMAL",
		LogLevel:                      "info",
		LogToFile:                     true,
		LogRetentionDays:              7,
		DashboardLayout:               "cards",
		DashboardRefreshSeconds:       30,
		Theme:                         "auto",
	}
}

func getEnv(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}

func envOr(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func envOrInt(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	out, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return out
}

func envOrBool(key string, fallback bool) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	out, err := strconv.ParseBool(val)
	if err != nil {
		return fallback
	}
	return out
}
