package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"golang.org/x/crypto/bcrypt"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
	"chowkidar/internal/crypto"
	"chowkidar/internal/dockerhealth"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
	"chowkidar/internal/worker"
)

// hostEntry pairs a configured Docker host profile with its live client.
type hostEntry struct {
	profile config.DockerHostProfile
	client  *dockerhealth.Client
}

type sessionEntry struct {
	username  string
	expiresAt time.Time
}

type app struct {
	cfg                  config.Config
	docker               dockerClient
	extraClients         []*hostEntry
	notifier             notifierClient
	pool                 *worker.Pool
	diagnostics          dockerhealth.Diagnostics
	selfID               string
	selfName             string
	lastNotified         map[string]time.Time
	actionCycle          map[string]int
	retriesExhausted     map[string]bool
	postActionDeadline   map[string]time.Time
	activeJobs           map[string]bool
	lastJobScan          map[string]time.Time
	lastJobNotifications map[string][]string
	lastJobRuleName      map[string]string
	knownNames           map[string]string
	httpClient           *http.Client
	jobs                 []config.Job
	notifications        []config.NotificationProfile
	notifUsage           map[string]*notifProfileUsage // profile ID → in-memory usage (reset on restart)
	notifUsageMu         sync.Mutex
	dockerHosts          []config.DockerHostProfile
	scripts              []config.ScriptEntry
	history              *history.Store
	mu                   sync.RWMutex
	lastScan             time.Time
	monitorStartsAt      time.Time
	unhealthy            []containertypes.Summary
	exited               []containertypes.Summary
	restarting           []containertypes.Summary
	totalExited          int
	authCfg              *config.AuthConfig
	sessions             map[string]sessionEntry
	sessionMu            sync.Mutex
	dryRunCleanupMu      sync.Mutex
	dryRunCleanupTimer   *time.Timer
	latestVersion        string               // non-empty when a newer release is found
	latestVersionChecked time.Time            // when the check last ran
	latestVersionRelDate string               // release date of latest version
	bootTime             time.Time            // set once at startup; used to key the monitoring-started alert
	clientTimezone       string               // last browser-reported IANA zone; reused when DisplayTimezone is unset
	dismissedAlerts      map[string]time.Time // alert ID → time dismissed; persisted to dismissed-alerts.json
	dismissedMu          sync.Mutex
}

type dockerClient interface {
	UnhealthyContainers(ctx context.Context) ([]containertypes.Summary, error)
	ExitedContainers(ctx context.Context) ([]containertypes.Summary, error)
	RestartingContainers(ctx context.Context) ([]containertypes.Summary, error)
	StartingContainers(ctx context.Context) ([]containertypes.Summary, error)
	AllContainers(ctx context.Context) ([]containertypes.Summary, error)
	RestartContainer(ctx context.Context, id string, timeoutSeconds int) error
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeoutSeconds int) error
	InspectContainer(ctx context.Context, id string) (containertypes.InspectResponse, error)
	Ping(ctx context.Context) error
}

type notifierClient interface {
	Send(title, body string) error
}

type actionJob struct {
	app                   *app
	docker                dockerClient
	container             containertypes.Summary
	reason                string
	action                string
	script                string
	notifications         []string
	jobID                 string
	jobName               string
	force                 bool
	retryCount            int
	actionTimeoutSeconds  int
	scriptTimeoutSeconds  int
	postActionWaitSeconds int
}

// appVersion is set at build time via -ldflags "-X main.appVersion=x.y.z".
// Falls back to "dev" when built without the flag (local development).
var appVersion = "dev"

const githubReleasesURL = "https://api.github.com/repos/darkmatterforge/chowkidar/releases/latest"

type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarn
	levelError
)

var (
	configuredLogLevel = levelInfo
)

func initLogging() {
	configuredLogLevel = parseLogLevel(os.Getenv("LOG_LEVEL"))
}

func parseLogLevel(raw string) logLevel {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return levelDebug
	case "warn", "warning":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

func logLevelLabel(level logLevel) string {
	switch level {
	case levelDebug:
		return "DEBUG"
	case levelWarn:
		return "WARN"
	case levelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func shouldLog(level logLevel) bool {
	return level >= configuredLogLevel
}

func logAt(level logLevel, format string, args ...any) {
	if !shouldLog(level) {
		return
	}
	log.Printf("[%s] "+format, append([]any{logLevelLabel(level)}, args...)...)
}

func logDebugf(format string, args ...any) {
	logAt(levelDebug, format, args...)
}

func logInfof(format string, args ...any) {
	logAt(levelInfo, format, args...)
}

func logWarnf(format string, args ...any) {
	logAt(levelWarn, format, args...)
}

func logErrorf(format string, args ...any) {
	logAt(levelError, format, args...)
}

// rotatingLogWriter tees log output to stdout and a daily-rotated file under dir.
type rotatingLogWriter struct {
	mu          sync.Mutex
	dir         string
	currentDate string
	file        *os.File
}

var globalLogWriter *rotatingLogWriter

func (w *rotatingLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = os.Stdout.Write(p)
	today := time.Now().Format("2006-01-02")
	if w.file == nil || w.currentDate != today {
		if w.file != nil {
			_ = w.file.Close()
			w.file = nil
		}
		path := filepath.Join(w.dir, "app-"+today+".log")
		if f, ferr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); ferr == nil {
			w.file = f
			w.currentDate = today
		}
	}
	if w.file != nil {
		_, _ = w.file.Write(p)
	}
	return len(p), nil
}

func (w *rotatingLogWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
}

func pruneOldLogs(logsDir string, retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "app-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		dateStr := strings.TrimPrefix(strings.TrimSuffix(name, ".log"), "app-")
		t, terr := time.Parse("2006-01-02", dateStr)
		if terr != nil {
			continue
		}
		if t.Before(cutoff) {
			if err := os.Remove(filepath.Join(logsDir, name)); err != nil {
				logWarnf("logs: failed to prune file=%s err=%v", name, err)
			} else {
				logInfof("logs: pruned file=%s", name)
			}
		}
	}
}

func applyLogConfig(logsDir string, cfg config.Config) {
	if cfg.LogLevel != "" {
		configuredLogLevel = parseLogLevel(cfg.LogLevel)
	}
	if cfg.LogToFile {
		if globalLogWriter == nil {
			globalLogWriter = &rotatingLogWriter{dir: logsDir}
		} else {
			globalLogWriter.mu.Lock()
			globalLogWriter.dir = logsDir
			globalLogWriter.mu.Unlock()
		}
		log.SetOutput(globalLogWriter)
		if cfg.LogRetentionDays > 0 {
			pruneOldLogs(logsDir, cfg.LogRetentionDays)
		}
	} else {
		log.SetOutput(os.Stdout)
		if globalLogWriter != nil {
			globalLogWriter.close()
			globalLogWriter = nil
		}
	}
}

func resolveJobSettings(j actionJob, cfg config.Config) (retryCount, actionTimeout int) {
	if j.jobID == "" {
		return cfg.RetryCount, cfg.ActionTimeoutSeconds
	}
	// retryCount == 0 means unlimited; keep it as-is.
	at := j.actionTimeoutSeconds
	if at < 1 {
		at = cfg.ActionTimeoutSeconds
	}
	return j.retryCount, at
}

func (a *app) notifyRetriesExhausted(j actionJob, name, action string, cycle, retryCount int, failed bool) {
	a.setRetriesExhausted(name, true)
	a.logActionHistory(j.container, j.reason, action, cycle, "exhausted", "monitoring paused — max retries reached", "", 0)
	event := "retry limit reached"
	msg := fmt.Sprintf("Container %s monitoring paused after %d cycles (%s)", name, cycle, j.reason)
	if failed {
		event = "action failed"
		msg = fmt.Sprintf("Container %s action %s failed after %d cycles (%s)", name, action, cycle, j.reason)
	}
	if a.shouldNotify(name) {
		if err := a.sendNotification(event, msg); err != nil {
			logWarnf("notify: send failed event=%s container=%s err=%v", event, name, err)
		}
		if err := a.sendJobNotifications(event, notifData{
			ContainerName: name,
			RuleName:      j.jobName,
			Action:        action,
			Reason:        j.reason,
			Cycle:         cycle,
			MaxRetries:    retryCount,
		}, j.notifications); err != nil {
			logWarnf("notify: job send failed event=%s container=%s err=%v", event, name, err)
		}
	}
}

func (j actionJob) Run(ctx context.Context) {
	name := containerName(j.container)
	cfg := j.app.getConfig()
	action := strings.ToLower(strings.TrimSpace(j.action))
	if action == "" {
		action = strings.ToLower(cfg.Action)
	}

	retryCount, actionTimeout := resolveJobSettings(j, cfg)
	postWait := time.Duration(j.postActionWaitSeconds) * time.Second

	source := "global-config"
	if j.jobID != "" {
		source = fmt.Sprintf("job:%s(%s)", j.jobName, j.jobID)
	}

	if j.force {
		logInfof("job: force-run container=%s action=%s source=%s", name, action, source)
		j.app.resetActionCooldown(name)
	}

	if !j.force {
		if !j.app.claimActiveJob(name) {
			logInfof("job: skipping container=%s reason=job-already-running source=%s", name, source)
			return
		}
		defer j.app.releaseActiveJob(name)

		if j.app.isRetriesExhausted(name) {
			logInfof("job: skipping container=%s reason=retries-exhausted source=%s", name, source)
			j.app.logActionHistory(j.container, j.reason, action, 0, "skipped", "retries exhausted", "", 0)
			return
		}

		// Queue-lag guard: re-check post-action wait after claiming the slot
		if remaining := j.app.postActionWaitRemaining(name); remaining > 0 {
			logInfof("job: skipping container=%s reason=post-action-wait remaining=%s source=%s",
				name, remaining.Round(time.Second), source)
			j.app.logActionHistory(j.container, j.reason, action, 0, "skipped", "cooldown active", "", 0)
			return
		}
	}

	// Increment cycle BEFORE acting so history reflects the correct attempt number
	cycle := j.app.incrementActionCycle(name)
	logInfof("job: start container=%s reason=%s action=%s cycle=%d/%d actionTimeout=%ds postWait=%s source=%s",
		name, j.reason, action, cycle, retryCount, actionTimeout, postWait, source)

	// Notify each retry attempt as it starts.
	if err := j.app.sendJobNotifications("retrying", notifData{
		ContainerName: name,
		RuleName:      j.jobName,
		Action:        action,
		Reason:        j.reason,
		Cycle:         cycle,
		MaxRetries:    retryCount,
	}, j.notifications); err != nil {
		logWarnf("notify: job send failed event=retrying container=%s err=%v", name, err)
	}

	started := time.Now()
	docker := j.docker
	if docker == nil {
		docker = j.app.docker
	}
	scriptOutput, err := j.app.executeAction(ctx, docker, action, j.script, j.container, actionTimeout, j.scriptTimeoutSeconds)
	duration := time.Since(started)

	if err == nil {
		// Action succeeded — apply post-action wait to give the container time to stabilise
		// before the next scan checks it again.
		if postWait > 0 {
			j.app.setPostActionDeadline(name, time.Now().Add(postWait))
			logInfof("job: post-action wait scheduled container=%s window=%s source=%s", name, postWait, source)
			if err := j.app.sendJobNotifications("cooldown", notifData{
				ContainerName: name,
				RuleName:      j.jobName,
				Cooldown:      j.postActionWaitSeconds,
			}, j.notifications); err != nil {
				logWarnf("notify: job send failed event=cooldown container=%s err=%v", name, err)
			}
		}
		if scriptOutput != "" {
			logInfof("job: script output container=%s:\n%s", name, scriptOutput)
		}
		logInfof("job: success container=%s action=%s cycle=%d/%d duration=%s source=%s",
			name, action, cycle, retryCount, duration.Round(time.Millisecond), source)
		j.app.logActionHistory(j.container, j.reason, action, cycle, "success", "", scriptOutput, duration)
		if j.force {
			// Manual action succeeded — reset cycle so auto-monitoring resumes cleanly.
			j.app.resetActionCooldown(name)
			return
		}
		if retryCount > 0 && cycle >= retryCount {
			logErrorf("job: retries exhausted after api-success container=%s action=%s cycle=%d/%d source=%s",
				name, action, cycle, retryCount, source)
			j.app.notifyRetriesExhausted(j, name, action, cycle, retryCount, false)
			return
		}
		if j.jobID != "" {
			j.app.mu.Lock()
			if j.app.lastJobNotifications != nil {
				j.app.lastJobNotifications[name] = j.notifications
			}
			if j.app.lastJobRuleName != nil {
				j.app.lastJobRuleName[name] = j.jobName
			}
			j.app.mu.Unlock()
		}
		return
	}

	logWarnf("job: failed container=%s action=%s cycle=%d/%d duration=%s err=%v source=%s",
		name, action, cycle, retryCount, duration.Round(time.Millisecond), err, source)
	j.app.logActionHistory(j.container, j.reason, action, cycle, "failed", err.Error(), scriptOutput, duration)
	if !j.force && retryCount > 0 && cycle >= retryCount {
		logErrorf("job: retries exhausted container=%s action=%s cycle=%d/%d source=%s",
			name, action, cycle, retryCount, source)
		j.app.notifyRetriesExhausted(j, name, action, cycle, retryCount, true)
	}
}

type bootFiles struct {
	jobs                 []config.Job
	notificationProfiles []config.NotificationProfile
	dockerHostProfiles   []config.DockerHostProfile
	scriptEntries        []config.ScriptEntry
}

func bootLoadFiles(cfg config.Config) (bootFiles, error) {
	var f bootFiles

	jobs, err := config.LoadJobs(cfg.ConfigDir)
	if err != nil {
		return f, fmt.Errorf("load jobs: %w", err)
	}
	if err := config.SaveJobs(cfg.ConfigDir, jobs); err != nil {
		return f, fmt.Errorf("init jobs file: %w", err)
	}
	logInfof("boot: jobs loaded count=%d path=%s", len(jobs), filepath.Join(cfg.ConfigDir, "jobs.yaml"))
	if err := config.InitDefaultTemplates(cfg.ConfigDir); err != nil {
		logWarnf("boot: could not initialise default templates file: %v", err)
	}

	notificationProfiles, err := config.LoadNotificationProfiles(cfg.ConfigDir)
	if err != nil {
		return f, fmt.Errorf("load notification profiles: %w", err)
	}
	if err := config.SaveNotificationProfiles(cfg.ConfigDir, notificationProfiles); err != nil {
		return f, fmt.Errorf("init notifications file: %w", err)
	}
	logInfof("boot: notifications loaded count=%d path=%s", len(notificationProfiles), filepath.Join(cfg.ConfigDir, "notifications.yaml"))

	dockerHostProfiles, err := config.LoadDockerHostProfiles(cfg.ConfigDir)
	if err != nil {
		return f, fmt.Errorf("load docker host profiles: %w", err)
	}
	if err := config.SaveDockerHostProfiles(cfg.ConfigDir, dockerHostProfiles); err != nil {
		return f, fmt.Errorf("init docker hosts file: %w", err)
	}
	logInfof("boot: docker hosts loaded count=%d path=%s", len(dockerHostProfiles), filepath.Join(cfg.ConfigDir, "docker_hosts.yaml"))

	scriptEntries, err := config.LoadScriptEntries(cfg.ConfigDir)
	if err != nil {
		return f, fmt.Errorf("load script entries: %w", err)
	}
	if err := config.SaveScriptEntries(cfg.ConfigDir, scriptEntries); err != nil {
		return f, fmt.Errorf("init scripts file: %w", err)
	}
	logInfof("boot: script entries loaded count=%d path=%s", len(scriptEntries), filepath.Join(cfg.ConfigDir, "scripts.yaml"))

	f.jobs = jobs
	f.notificationProfiles = notificationProfiles
	f.dockerHostProfiles = dockerHostProfiles
	f.scriptEntries = scriptEntries
	return f, nil
}

func main() {
	initLogging()
	logInfof("boot: logging initialized logLevel=%s", logLevelLabel(configuredLogLevel))

	if key := os.Getenv("CHOWKIDAR_SECRET_KEY"); key != "" {
		crypto.SetKey(key)
		logInfof("boot: encryption enabled (CHOWKIDAR_SECRET_KEY set)")
	} else {
		logInfof("boot: encryption disabled (CHOWKIDAR_SECRET_KEY not set — secrets stored as plaintext)")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	logInfof("boot: config loaded port=%s configDir=%s action=%s workerCount=%d queueSize=%d", cfg.Port, cfg.ConfigDir, cfg.Action, cfg.WorkerCount, cfg.QueueSize)

	if err := os.MkdirAll(filepath.Join(cfg.ConfigDir, "data"), 0o755); err != nil {
		log.Fatalf("failed to create config data dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.ConfigDir, "logs"), 0o755); err != nil {
		log.Fatalf("failed to create config logs dir: %v", err)
	}
	logsDir := filepath.Join(cfg.ConfigDir, "logs")
	logInfof("boot: config directories ensured dataDir=%s logsDir=%s", filepath.Join(cfg.ConfigDir, "data"), logsDir)
	applyLogConfig(logsDir, cfg)
	logInfof("boot: log config applied logLevel=%s logToFile=%t logRetentionDays=%d", cfg.LogLevel, cfg.LogToFile, cfg.LogRetentionDays)
	if err := ensureConfigFile(cfg); err != nil {
		log.Fatalf("failed to initialize config file: %v", err)
	}
	logInfof("boot: config file ensured path=%s", filepath.Join(cfg.ConfigDir, "config.yaml"))
	files, err := bootLoadFiles(cfg)
	if err != nil {
		log.Fatalf("boot: %v", err)
	}
	if svc := buildNotifierServicesFromProfiles(files.notificationProfiles); svc != "" {
		cfg.AppriseServices = svc
	}

	if crypto.KeyConfigured() {
		migrateSecretsToEncrypted(cfg.ConfigDir, files.notificationProfiles, files.dockerHostProfiles)
	}

	historyStore, err := history.NewStore(cfg.ConfigDir)
	if err != nil {
		log.Fatalf("failed to initialize history store: %v", err)
	}
	logInfof("boot: history store initialized path=%s", historyStore.Path())

	authCfg, err := config.LoadAuthConfig(cfg.ConfigDir)
	if err != nil {
		log.Fatalf("failed to load auth config: %v", err)
	}
	if authCfg.Enabled {
		logInfof("boot: auth enabled username=%s", authCfg.Username)
	} else {
		logInfof("boot: auth disabled")
	}

	dockerClient, err := dockerhealth.NewClient()
	if err != nil {
		log.Fatalf("failed to init docker client: %v", err)
	}
	defer func() { _ = dockerClient.Close() }()
	logInfof("boot: docker client initialized")

	notifier := notify.New(cfg.AppriseServices)
	pool := worker.NewPool(cfg.WorkerCount, cfg.QueueSize)

	a := &app{
		cfg:                  cfg,
		docker:               dockerClient,
		notifier:             notifier,
		pool:                 pool,
		jobs:                 files.jobs,
		notifications:        files.notificationProfiles,
		dockerHosts:          files.dockerHostProfiles,
		scripts:              files.scriptEntries,
		history:              historyStore,
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
		authCfg:              authCfg,
		sessions:             make(map[string]sessionEntry),
		bootTime:             time.Now().UTC(),
		dismissedAlerts:      make(map[string]time.Time),
	}
	defer a.stopPool()
	if da, err := loadDismissedAlerts(cfg.ConfigDir); err != nil {
		logWarnf("boot: could not load dismissed alerts: %v", err)
	} else {
		a.dismissedAlerts = da
		logInfof("boot: dismissed alerts loaded count=%d", len(da))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = cfg.HttpMaxIdleConns
	transport.MaxIdleConnsPerHost = cfg.HttpMaxIdleConnsPerHost
	transport.IdleConnTimeout = time.Duration(cfg.HttpIdleConnTimeoutSeconds) * time.Second
	a.httpClient = &http.Client{
		Timeout:   time.Duration(cfg.HttpClientTimeoutSeconds) * time.Second,
		Transport: transport,
	}
	a.selfID, a.selfName = detectSelfIdentity(a)
	logInfof("boot: self identity detected selfID=%s selfName=%s", a.selfID, a.selfName)

	ctx := context.Background()
	logInfof("boot: running startup docker diagnostics mode=socket socketPath=%s pingTimeoutSeconds=%d", cfg.DockerSocketPath, cfg.DockerPingTimeoutSeconds)
	_ = a.retryDocker(ctx, func() error {
		a.diagnostics = dockerhealth.SocketDiagnostics(ctx, dockerClient, cfg.DockerSocketPath, cfg.DockerPingTimeoutSeconds)
		if a.diagnostics.DockerReachable {
			return nil
		}
		return errors.New(a.diagnostics.Details)
	})
	logInfof("boot: startup diagnostics mode=%s reachable=%t socketPresent=%t socketWritable=%t details=%q", a.diagnostics.Mode, a.diagnostics.DockerReachable, a.diagnostics.SocketPresent, a.diagnostics.SocketWritable, a.diagnostics.Details)
	if !a.diagnostics.DockerReachable {
		logWarnf("docker diagnostics warning: %s", a.diagnostics.Details)
	}
	if !a.diagnostics.SocketWritable {
		logWarnf("docker socket not writable: %s", a.diagnostics.Details)
	}

	stopMonitor := make(chan struct{})
	a.buildExtraClients()
	logInfof("boot: extra docker hosts initialized count=%d", len(a.getExtraClients()))
	go a.monitorLoop(stopMonitor)
	logInfof("boot: monitor loop started")
	go a.checkForUpdates(stopMonitor)
	logInfof("boot: update checker started version=%s", appVersion)
	go a.pruneHistoryLoop(stopMonitor)
	logInfof("boot: history pruner started retentionDays=%d", cfg.LogRetentionDays)

	mux := setupMux(a)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	srvErrCh := startHTTPServer(srv)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-srvErrCh:
		logErrorf("server error: %v", err)
		os.Exit(1)
	case <-sigCh:
	}
	logInfof("shutdown signal received")
	a.cancelDryRunCleanup()
	shutdownApplication(stopMonitor, srv, 15*time.Second)
	logInfof("shutdown: application stopped")
}

// noDirFS wraps http.Dir to prevent directory listing: Open returns ErrNotExist
// for any directory path that is not the root (so FileServer can still find index.html).
type noDirFS struct{ root http.Dir }

func (n noDirFS) Open(name string) (http.File, error) {
	f, err := n.root.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if stat.IsDir() && name != "/" {
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

func setupMux(a *app) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/status", a.handleAuthStatus)
	mux.HandleFunc("/api/auth/login", a.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", a.handleAuthLogout)
	mux.HandleFunc("/api/auth/change-password", a.authMiddleware(a.handleAuthChangePassword))
	mux.HandleFunc("/api/auth/disable", a.authMiddleware(a.handleAuthDisable))
	mux.HandleFunc("/api/health", a.handleHealth) // no auth — needed for Docker HEALTHCHECK
	mux.HandleFunc("/api/diagnostics", a.authMiddleware(a.handleDiagnostics))
	mux.HandleFunc("/api/containers", a.authMiddleware(a.handleContainers))
	mux.HandleFunc("/api/settings", a.authMiddleware(a.handleSettings))
	mux.HandleFunc("/api/settings/theme", a.authMiddleware(a.handleSettingsTheme))
	mux.HandleFunc("/api/scripts/dry-run", a.authMiddleware(a.handleScriptDryRun))
	mux.HandleFunc("/api/scripts/dry-run/cleanup", a.authMiddleware(a.handleScriptDryRunCleanup))
	mux.HandleFunc("/api/jobs", a.authMiddleware(a.handleJobs))
	mux.HandleFunc("/api/jobs/", a.authMiddleware(a.handleJobByID))
	mux.HandleFunc("/api/notifications", a.authMiddleware(a.handleNotifications))
	mux.HandleFunc("/api/notifications/", a.authMiddleware(a.handleNotificationByID))
	mux.HandleFunc("/api/system-alerts", a.authMiddleware(a.handleSystemAlerts))
	mux.HandleFunc("/api/system-alerts/dismiss", a.authMiddleware(a.handleSystemAlertsDismiss))
	mux.HandleFunc("/api/docker-hosts", a.authMiddleware(a.handleDockerHosts))
	mux.HandleFunc("/api/docker-hosts/status", a.authMiddleware(a.handleDockerHostsStatus))
	mux.HandleFunc("/api/scripts", a.authMiddleware(a.handleScripts))
	mux.HandleFunc("/api/history", a.authMiddleware(a.handleHistoryEndpoint))
	mux.HandleFunc("/api/test-notification", a.authMiddleware(a.handleTestNotification))
	mux.HandleFunc("/api/test-docker-host", a.authMiddleware(a.handleTestDockerHost))
	mux.HandleFunc("/api/action", a.authMiddleware(a.handleManualAction))
	mux.HandleFunc("/api/containers/", a.authMiddleware(a.handleResetCooldown))
	// noDirFS wraps http.Dir and returns ErrNotExist for any directory path,
	// preventing directory listing. The FileServer call is safe here. //nolint:gosec
	fileServer := http.FileServer(noDirFS{http.Dir("web")})
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		fileServer.ServeHTTP(w, r)
	}))
	return mux
}

func startHTTPServer(srv *http.Server) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		logInfof("server listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	return errCh
}

func (a *app) cancelDryRunCleanup() {
	a.dryRunCleanupMu.Lock()
	defer a.dryRunCleanupMu.Unlock()
	if a.dryRunCleanupTimer != nil {
		a.dryRunCleanupTimer.Stop()
		a.dryRunCleanupTimer = nil
	}
}

func shutdownApplication(stopMonitor chan struct{}, srv *http.Server, timeout time.Duration) {
	logInfof("shutdown: initiating graceful stop timeout=%s", timeout)
	close(stopMonitor)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logErrorf("http shutdown error: %v", err)
		return
	}
	logInfof("shutdown: http server stopped cleanly")
}

func shouldSendStartupNotification(d dockerhealth.Diagnostics) bool {
	return d.DockerReachable
}

func (a *app) sendStartupNotificationIfReady() error {
	cfg := a.getConfig()
	if !cfg.BootNotification {
		return nil
	}
	if !shouldSendStartupNotification(a.diagnostics) {
		logWarnf("startup notification skipped: diagnostics not ready (%s)", strings.TrimSpace(a.diagnostics.Details))
		return nil
	}
	if len(cfg.BootNotificationProfiles) > 0 {
		return a.sendJobNotifications("startup", notifData{}, cfg.BootNotificationProfiles)
	}
	return a.sendNotification("startup", "Application started and monitor loop initialized")
}

func (a *app) monitorLoop(stop <-chan struct{}) {
	if d := a.getConfig().StartupDelaySeconds; d > 0 {
		logInfof("monitor: startup delay seconds=%d", d)
		startsAt := time.Now().Add(time.Duration(d) * time.Second)
		a.mu.Lock()
		a.monitorStartsAt = startsAt
		a.mu.Unlock()
		select {
		case <-stop:
			a.mu.Lock()
			a.monitorStartsAt = time.Time{}
			a.mu.Unlock()
			logInfof("monitor: stop received during startup delay")
			return
		case <-time.After(time.Duration(d) * time.Second):
			a.mu.Lock()
			a.monitorStartsAt = time.Time{}
			a.mu.Unlock()
			logInfof("monitor: startup delay elapsed, beginning monitor")
		}
	}
	_ = a.sendStartupNotificationIfReady()
	for {
		logDebugf("monitor: scan cycle start")
		a.scanOnce()
		sleep := a.timeUntilNextJobDue()
		logDebugf("monitor: next scan in %s", sleep.Round(time.Second))
		select {
		case <-stop:
			logInfof("monitor: stop signal received, exiting loop")
			return
		case <-time.After(sleep):
		}
	}
}

func (a *app) timeUntilNextJobDue() time.Duration {
	jobs := a.getJobs()
	a.mu.RLock()
	defer a.mu.RUnlock()

	now := time.Now()
	var next time.Time
	for _, r := range jobs {
		if !r.Enabled {
			continue
		}
		interval := time.Duration(r.MonitorIntervalSeconds) * time.Second
		if interval < 5*time.Second {
			interval = 60 * time.Second
		}
		due := a.lastJobScan[r.ID].Add(interval)
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	if next.IsZero() {
		return 60 * time.Second // no jobs: check back in 60s in case jobs are added
	}
	if d := next.Sub(now); d > 0 {
		return d
	}
	return time.Second // already overdue, fire immediately after 1s floor
}

func (a *app) retryDocker(ctx context.Context, fn func() error) error {
	cfg := a.getConfig()
	if cfg.DockerClientRetryCount < 1 {
		cfg.DockerClientRetryCount = 1
	}
	if cfg.DockerClientRetryDelaySeconds < 1 {
		cfg.DockerClientRetryDelaySeconds = 2
	}
	for attempt := 1; attempt <= cfg.DockerClientRetryCount; attempt++ {
		if err := ctx.Err(); err != nil {
			logWarnf("docker: retry aborted contextErr=%v", err)
			return err
		}
		if attempt > 1 {
			logDebugf("docker: retry attempt %d/%d", attempt, cfg.DockerClientRetryCount)
		}
		if err := fn(); err == nil {
			if attempt > 1 {
				logInfof("docker: operation succeeded after retries attempts=%d", attempt)
			}
			return nil
		} else {
			logWarnf("docker: operation failed attempt %d/%d err=%v", attempt, cfg.DockerClientRetryCount, err)
			if attempt < cfg.DockerClientRetryCount {
				time.Sleep(time.Duration(cfg.DockerClientRetryDelaySeconds) * time.Second)
			}
		}
	}
	return fmt.Errorf("docker operation failed after %d attempts", cfg.DockerClientRetryCount)
}

// notifProfileUsage tracks in-memory send counts for rate limiting.
// Counts reset on server restart — this is acceptable since SuspendedUntil
// (the hard enforcement) is persisted to notifications.yaml.
type notifProfileUsage struct {
	dayKey     string // "YYYY-MM-DD" — reset when day changes
	dayCount   int
	burstKey   string // truncated time bucket
	burstCount int
}

type recoveredContainer struct {
	name          string
	notifications []string
	ruleName      string
}

// applyMatchedJobSettings copies per-job overrides from matched into job.
func applyMatchedJobSettings(job *actionJob, matched *config.Job) {
	if matched == nil {
		return
	}
	job.retryCount = effectiveRetryCount(matched)
	if matched.ActionTimeoutSeconds > 0 {
		job.actionTimeoutSeconds = matched.ActionTimeoutSeconds
	}
	if matched.ScriptTimeoutSeconds > 0 {
		job.scriptTimeoutSeconds = matched.ScriptTimeoutSeconds
	}
	job.postActionWaitSeconds = matched.PostActionWaitSeconds
	if job.postActionWaitSeconds <= 0 {
		job.postActionWaitSeconds = matched.MonitorIntervalSeconds
	}
}

// computeDueJobs returns the subset of jobs whose scan interval has elapsed and
// updates a.lastJobScan under the write lock.
func (a *app) computeDueJobs(jobs []config.Job) ([]config.Job, map[string]bool) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	dueJobs := make([]config.Job, 0, len(jobs))
	dueJobIDs := make(map[string]bool, len(jobs))
	for _, r := range jobs {
		if !r.Enabled {
			continue
		}
		interval := time.Duration(r.MonitorIntervalSeconds) * time.Second
		if interval < 5*time.Second {
			interval = 60 * time.Second
		}
		last, seen := a.lastJobScan[r.ID]
		if !seen || now.Sub(last) >= interval {
			dueJobs = append(dueJobs, r)
			dueJobIDs[r.ID] = true
			a.lastJobScan[r.ID] = now
		}
	}
	return dueJobs, dueJobIDs
}

func (a *app) processUnhealthyContainers(ctx context.Context, docker dockerClient, hostID string, containers []containertypes.Summary, cfg config.Config, jobs []config.Job, dueJobIDs map[string]bool) []containertypes.Summary {
	filtered := make([]containertypes.Summary, 0, len(containers))
	for _, c := range containers {
		name := containerName(c)
		logInfof("monitor: evaluating unhealthy container=%s id=%.12s state=%s status=%s", name, c.ID, c.State, c.Status)
		if a.isSelfContainer(c) {
			logDebugf("monitor: skip self container=%s id=%.12s", name, c.ID)
			continue
		}
		selectedAction, jobScript, jobNotifications, jobID, jobName, matchedJob, matched := a.selectActionForContainer(ctx, docker, c, cfg, jobs)
		if !matched {
			logDebugf("monitor: no job/filter match container=%s — skipping", name)
			continue
		}
		filtered = append(filtered, c)
		if !dueJobIDs[jobID] {
			logDebugf("monitor: job not due container=%s jobID=%s", name, jobID)
			continue
		}
		if a.isRetriesExhausted(name) {
			logInfof("monitor: skip container=%s reason=retries-exhausted", name)
			continue
		}
		if remaining := a.postActionWaitRemaining(name); remaining > 0 {
			logInfof("monitor: skip container=%s reason=post-action-wait remaining=%s", name, remaining.Round(time.Second))
			a.logActionHistory(c, "unhealthy", selectedAction, 0, "skipped", "cooldown active", "", 0)
			continue
		}
		job := actionJob{
			app: a, container: c, reason: "unhealthy",
			action: selectedAction, script: jobScript, notifications: jobNotifications,
			jobID: jobID, jobName: jobName,
		}
		applyMatchedJobSettings(&job, matchedJob)
		logInfof("monitor: queueing unhealthy container=%s action=%s jobID=%s jobName=%s retryCount=%d postWait=%ds",
			name, selectedAction, jobID, jobName, job.retryCount, job.postActionWaitSeconds)
		a.enqueueJob(job)
		if a.shouldNotify(name) {
			if err := a.sendJobNotifications("unhealthy detected", notifData{
				ContainerName: name,
				RuleName:      jobName,
				Reason:        "unhealthy",
				MaxRetries:    job.retryCount,
			}, jobNotifications); err != nil {
				logWarnf("notify: job send failed event=unhealthy container=%s err=%v", name, err)
			}
		}
	}
	return filtered
}

// processRestartingContainers queries stuck-restarting containers, enqueues actions for
// chowkidar-managed ones, and appends matched ones to *filtered.
func (a *app) processRestartingContainers(ctx context.Context, docker dockerClient, hostID string, cfg config.Config, jobs []config.Job, dueJobIDs map[string]bool, filtered *[]containertypes.Summary) []containertypes.Summary {
	var restartingContainers []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		restartingContainers, err = docker.RestartingContainers(ctx)
		return err
	}); err != nil {
		logErrorf("failed to query restarting containers: %v", err)
	}
	for _, c := range restartingContainers {
		name := containerName(c)
		if a.isSelfContainer(c) {
			continue
		}
		a.mu.RLock()
		inCycle := a.actionCycle[name] > 0
		a.mu.RUnlock()
		if !inCycle {
			continue
		}
		logInfof("monitor: evaluating stuck-restarting container=%s id=%.12s", name, c.ID)
		selectedAction, jobScript, jobNotifications, jobID, jobName, matchedJob, matched := a.selectActionForContainer(ctx, docker, c, cfg, jobs)
		if !matched {
			continue
		}
		*filtered = append(*filtered, c)
		if !dueJobIDs[jobID] {
			continue
		}
		if a.isRetriesExhausted(name) {
			logInfof("monitor: skip container=%s reason=retries-exhausted (stuck-restarting)", name)
			continue
		}
		if remaining := a.postActionWaitRemaining(name); remaining > 0 {
			logInfof("monitor: skip container=%s reason=post-action-wait remaining=%s (stuck-restarting)", name, remaining.Round(time.Second))
			a.logActionHistory(c, "stuck-restarting", selectedAction, 0, "skipped", "cooldown active", "", 0)
			continue
		}
		job := actionJob{
			app: a, container: c, reason: "stuck-restarting",
			action: selectedAction, script: jobScript, notifications: jobNotifications,
			jobID: jobID, jobName: jobName,
		}
		applyMatchedJobSettings(&job, matchedJob)
		logInfof("monitor: queueing stuck-restarting container=%s action=%s jobID=%s retryCount=%d postWait=%ds",
			name, selectedAction, jobID, job.retryCount, job.postActionWaitSeconds)
		a.enqueueJob(job)
	}
	return restartingContainers
}

func (a *app) processCrashedContainer(ctx context.Context, docker dockerClient, hostID string, c containertypes.Summary, cfg config.Config, jobs []config.Job, dueJobIDs map[string]bool, exitCode int) bool {
	name := containerName(c)
	reason := fmt.Sprintf("crash (exit %d)", exitCode)
	selectedAction, jobScript, jobNotifications, jobID, jobName, matchedJob, matched := a.selectActionForContainer(ctx, docker, c, cfg, jobs)
	if !matched {
		logDebugf("monitor: crashed container=%s exitCode=%d — no job match, skipping", name, exitCode)
		return false
	}
	if !dueJobIDs[jobID] {
		logDebugf("monitor: job not due container=%s jobID=%s", name, jobID)
		return true // matched but not due — still add to exitedFiltered
	}
	if a.isRetriesExhausted(name) {
		logInfof("monitor: skip container=%s reason=retries-exhausted", name)
		return true
	}
	if remaining := a.postActionWaitRemaining(name); remaining > 0 {
		logInfof("monitor: skip container=%s reason=post-action-wait remaining=%s", name, remaining.Round(time.Second))
		a.logActionHistory(c, reason, selectedAction, 0, "skipped", "cooldown active", "", 0)
		return true
	}
	job := actionJob{
		app: a, container: c, reason: reason,
		action: selectedAction, script: jobScript, notifications: jobNotifications,
		jobID: jobID, jobName: jobName,
	}
	applyMatchedJobSettings(&job, matchedJob)
	logInfof("monitor: queueing crashed container=%s exitCode=%d action=%s jobID=%s", name, exitCode, selectedAction, jobID)
	a.enqueueJob(job)
	return true
}

func (a *app) processExitedContainers(ctx context.Context, docker dockerClient, hostID string, cfg config.Config, jobs []config.Job, dueJobs []config.Job, dueJobIDs map[string]bool) []containertypes.Summary {
	exitedFiltered := make([]containertypes.Summary, 0)
	if !cfg.StartExited && len(jobs) == 0 {
		return exitedFiltered
	}
	if cfg.RequireFilterForExited && !hasAnyFilters(cfg) && !hasAnyJobFilters(jobs) {
		logWarnf("start-exited enabled but no filters configured; skipping exited container scan for safety")
		return exitedFiltered
	}
	var exited []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		exited, err = docker.ExitedContainers(ctx)
		return err
	}); err != nil {
		logErrorf("failed to query exited containers: %v", err)
		return exitedFiltered
	}
	logInfof("monitor: scan fetched exited=%d", len(exited))
	// Deduplicate by name: if multiple stopped containers share a name (old instances),
	// keep only the newest one to avoid duplicate processing and history entries.
	seen := make(map[string]bool, len(exited))
	cleanExitCount := 0
	for _, c := range exited {
		name := containerName(c)
		if a.isSelfContainer(c) {
			logDebugf("monitor: skip self exited container=%s id=%.12s", name, c.ID)
			continue
		}
		if seen[name] {
			logDebugf("monitor: skip duplicate exited container=%s id=%.12s", name, c.ID)
			continue
		}
		seen[name] = true
		exitCode := parseExitCode(c.Status)
		logInfof("monitor: evaluating exited container=%s id=%.12s state=%s status=%s exitCode=%d", name, c.ID, c.State, c.Status, exitCode)
		if exitCode == 0 {
			cleanExitCount++
			if a.shouldStartExitedContainer(ctx, docker, c, cfg, dueJobs) {
				exitedFiltered = append(exitedFiltered, c)
				logInfof("monitor: queueing exited container=%s action=start", name)
				a.enqueueJob(actionJob{app: a, container: c, reason: "exited", action: "start"})
			} else {
				logDebugf("monitor: no start-exited job/filter match container=%s — skipping", name)
			}
		} else if a.processCrashedContainer(ctx, docker, hostID, c, cfg, jobs, dueJobIDs, exitCode) {
			exitedFiltered = append(exitedFiltered, c)
		}
	}
	a.mu.Lock()
	a.totalExited = cleanExitCount
	a.mu.Unlock()
	return exitedFiltered
}

func (a *app) fetchStartingContainers(ctx context.Context, docker dockerClient) []containertypes.Summary {
	var starting []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		starting, err = docker.StartingContainers(ctx)
		return err
	}); err != nil {
		logErrorf("failed to query starting containers: %v", err)
	}
	return starting
}

// finalizeState builds the problematic container set, detects recoveries, updates
// shared state, and returns the list of just-recovered containers for notification.
func (a *app) finalizeState(filtered, exitedFiltered, restarting, starting []containertypes.Summary) ([]recoveredContainer, map[string]bool) {
	problematic := make(map[string]bool, len(filtered)+len(exitedFiltered)+len(restarting))
	for _, c := range filtered {
		problematic[containerName(c)] = true
	}
	for _, c := range exitedFiltered {
		problematic[containerName(c)] = true
	}
	for _, c := range restarting {
		problematic[containerName(c)] = true
	}

	var justRecovered []recoveredContainer
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range starting {
		name := containerName(c)
		if a.actionCycle[name] > 0 {
			problematic[name] = true
		}
	}
	for name := range a.actionCycle {
		if a.retriesExhausted[name] {
			continue
		}
		if !problematic[name] && a.postActionDeadline[name].Before(time.Now()) {
			logInfof("monitor: container recovered container=%s — resetting retry state", name)
			justRecovered = append(justRecovered, recoveredContainer{
				name:          name,
				notifications: a.lastJobNotifications[name],
				ruleName:      a.lastJobRuleName[name],
			})
			delete(a.actionCycle, name)
			delete(a.lastJobNotifications, name)
			delete(a.lastJobRuleName, name)
		}
	}
	a.unhealthy = filtered
	a.exited = exitedFiltered
	a.restarting = restarting
	a.lastScan = time.Now().UTC()
	return justRecovered, problematic
}

// matchingJobID returns the ID of the first enabled job that matches the container.
func matchingJobID(name string, labels map[string]string, jobs []config.Job) string {
	for _, r := range jobs {
		if r.Enabled && jobMatchesServiceSnapshot(name, labels, nil, r) {
			return r.ID
		}
	}
	return ""
}

func (a *app) collectHealthPulseTargets(allCtrs []containertypes.Summary, jobs []config.Job, dueJobIDs map[string]bool, problematic map[string]bool) []containertypes.Summary {
	var targets []containertypes.Summary
	for _, c := range allCtrs {
		name := containerName(c)
		if c.State != "running" || a.isSelfContainer(c) || problematic[name] {
			continue
		}
		jobID := matchingJobID(name, c.Labels, jobs)
		if jobID == "" || !dueJobIDs[jobID] {
			continue
		}
		targets = append(targets, c)
	}
	return targets
}

func (a *app) recordHealthPulses(ctx context.Context, docker dockerClient, jobs []config.Job, dueJobIDs map[string]bool, problematic map[string]bool) {
	var allCtrs []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		allCtrs, err = docker.AllContainers(ctx)
		return err
	}); err != nil {
		logWarnf("failed to query containers for health pulse: %v", err)
		return
	}
	a.mu.Lock()
	for _, c := range allCtrs {
		if c.ID != "" {
			a.knownNames[c.ID] = containerName(c)
		}
	}
	a.mu.Unlock()
	for _, c := range a.collectHealthPulseTargets(allCtrs, jobs, dueJobIDs, problematic) {
		a.logActionHistory(c, "health check", "healthy", 0, "healthy", "", "", 0)
	}
}

// jobsForHost returns jobs that apply to the given hostID — jobs with empty
// DockerHostID match every host; jobs with a specific ID match only that host.
func jobsForHost(jobs []config.Job, hostID string) []config.Job {
	out := make([]config.Job, 0, len(jobs))
	for _, j := range jobs {
		if j.DockerHostID == "" || j.DockerHostID == hostID {
			out = append(out, j)
		}
	}
	return out
}

// scanHost scans a single Docker host and returns the problematic container sets.
func (a *app) scanHost(ctx context.Context, hostID string, docker dockerClient, cfg config.Config, allJobs []config.Job, dueJobs []config.Job, dueJobIDs map[string]bool) (filtered, exitedFiltered, restarting, starting []containertypes.Summary) {
	jobs := jobsForHost(allJobs, hostID)
	dueForHost := make(map[string]bool, len(dueJobIDs))
	for id := range dueJobIDs {
		for _, j := range jobs {
			if j.ID == id {
				dueForHost[id] = true
				break
			}
		}
	}

	var unhealthyCtrs []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		unhealthyCtrs, err = docker.UnhealthyContainers(ctx)
		return err
	}); err != nil {
		logErrorf("docker-hosts: failed to query unhealthy containers host=%s: %v", hostID, err)
		return
	}

	logInfof("monitor: scan start host=%s unhealthy=%d totalJobs=%d dueJobs=%d",
		hostID, len(unhealthyCtrs), len(jobs), len(dueForHost))

	filtered = a.processUnhealthyContainers(ctx, docker, hostID, unhealthyCtrs, cfg, jobs, dueForHost)
	restarting = a.processRestartingContainers(ctx, docker, hostID, cfg, jobs, dueForHost, &filtered)
	exitedFiltered = a.processExitedContainers(ctx, docker, hostID, cfg, jobs, dueJobs, dueForHost)
	starting = a.fetchStartingContainers(ctx, docker)
	return
}

func (a *app) scanOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := a.getConfig()
	allJobs := a.getJobs()
	dueJobs, dueJobIDs := a.computeDueJobs(allJobs)

	// Scan primary host + all extra hosts, merging results.
	var allFiltered, allExited, allRestarting, allStarting []containertypes.Summary
	f, e, r, s := a.scanHost(ctx, "local", a.docker, cfg, allJobs, dueJobs, dueJobIDs)
	allFiltered = append(allFiltered, f...)
	allExited = append(allExited, e...)
	allRestarting = append(allRestarting, r...)
	allStarting = append(allStarting, s...)

	for _, h := range a.getExtraClients() {
		f, e, r, s = a.scanHost(ctx, h.profile.ID, h.client, cfg, allJobs, dueJobs, dueJobIDs)
		allFiltered = append(allFiltered, f...)
		allExited = append(allExited, e...)
		allRestarting = append(allRestarting, r...)
		allStarting = append(allStarting, s...)
	}

	justRecovered, problematic := a.finalizeState(allFiltered, allExited, allRestarting, allStarting)

	for _, ev := range justRecovered {
		c := containertypes.Summary{Names: []string{"/" + ev.name}}
		a.logActionHistory(c, "container recovered", "recovered", 0, "recovered", "", "", 0)
		msg := fmt.Sprintf("Container %s recovered and is healthy", ev.name)
		if a.shouldNotify(ev.name) {
			if err := a.sendNotification("container recovered", msg); err != nil {
				logWarnf("notify: send failed event=recovered container=%s err=%v", ev.name, err)
			}
			if err := a.sendJobNotifications("container recovered", notifData{
				ContainerName: ev.name,
				RuleName:      ev.ruleName,
			}, ev.notifications); err != nil {
				logWarnf("notify: job send failed event=recovered container=%s err=%v", ev.name, err)
			}
		}
	}

	// Record health pulses for each host.
	a.recordHealthPulses(ctx, a.docker, allJobs, dueJobIDs, problematic)
	for _, h := range a.getExtraClients() {
		a.recordHealthPulses(ctx, h.client, allJobs, dueJobIDs, problematic)
	}

	logInfof("monitor: scan complete unhealthyQueued=%d exitedQueued=%d hosts=%d",
		len(allFiltered), len(allExited), 1+len(a.getExtraClients()))
}

func (a *app) selectActionForContainer(ctx context.Context, docker dockerClient, c containertypes.Summary, cfg config.Config, jobs []config.Job) (action, script string, notifications []string, jobID, jobName string, matchedJob *config.Job, matched bool) {
	name := containerName(c)
	hasEnabledJobs := false
	for i, r := range jobs {
		if !r.Enabled {
			logDebugf("job: skip disabled jobID=%s jobName=%s container=%s", r.ID, r.Name, name)
			continue
		}
		hasEnabledJobs = true
		logDebugf("job: evaluating jobID=%s jobName=%s container=%s action=%s nameFilter=%q labelFilter=%q envFilter=%q notifications=%d",
			r.ID, r.Name, name, r.Action,
			r.ContainerNameFilter, r.ContainerLabelFilter, r.ContainerEnvVarFilter, len(r.Notifications))
		ok, err := a.matchesJob(ctx, docker, c, r)
		if err != nil {
			logWarnf("job: match error jobID=%s jobName=%s container=%s err=%v", r.ID, r.Name, name, err)
			continue
		}
		if ok {
			logInfof("job: matched jobID=%s jobName=%s container=%s action=%s notifications=%d",
				r.ID, r.Name, name, r.Action, len(r.Notifications))
			return r.Action, r.Script, r.Notifications, r.ID, r.Name, &jobs[i], true
		}
		logDebugf("job: no match jobID=%s jobName=%s container=%s", r.ID, r.Name, name)
	}

	if !hasEnabledJobs && hasAnyFilters(cfg) {
		logDebugf("job: no enabled jobs — checking global filters container=%s nameFilter=%q labelFilter=%q envFilter=%q",
			name, cfg.ContainerNameFilter, cfg.ContainerLabelFilter, cfg.ContainerEnvVarFilter)
		if a.matchesFilters(ctx, docker, c, cfg) {
			logInfof("job: matched global-config container=%s action=%s", name, cfg.Action)
			return cfg.Action, "", nil, "", "global-config", nil, true
		}
		logDebugf("job: global filter no match container=%s", name)
	}

	return "", "", nil, "", "", nil, false
}

func (a *app) matchesJob(ctx context.Context, docker dockerClient, c containertypes.Summary, r config.Job) (bool, error) {
	name := containerName(c)

	if r.ContainerNameFilter != "" {
		allowed := splitCSV(r.ContainerNameFilter)
		ok := false
		for _, x := range allowed {
			if strings.Contains(name, x) {
				ok = true
				break
			}
		}
		logDebugf("job: name-filter jobID=%s container=%s filter=%q containerName=%q match=%t",
			r.ID, name, r.ContainerNameFilter, name, ok)
		if !ok {
			return false, nil
		}
	}

	if r.ContainerLabelFilter != "" {
		ok := false
		for k, v := range c.Labels {
			if strings.Contains(k+"="+v, r.ContainerLabelFilter) {
				ok = true
				break
			}
		}
		logDebugf("job: label-filter jobID=%s container=%s filter=%q match=%t",
			r.ID, name, r.ContainerLabelFilter, ok)
		if !ok {
			return false, nil
		}
	}

	if r.ContainerEnvVarFilter != "" {
		inspected, err := docker.InspectContainer(ctx, c.ID)
		if err != nil {
			return false, err
		}
		ok := dockerhealth.ContainerHasEnvVar(inspected, r.ContainerEnvVarFilter)
		logDebugf("job: env-filter jobID=%s container=%s filter=%q match=%t",
			r.ID, name, r.ContainerEnvVarFilter, ok)
		if !ok {
			return false, nil
		}
	}

	return true, nil
}

func (a *app) shouldStartExitedContainer(ctx context.Context, docker dockerClient, c containertypes.Summary, cfg config.Config, jobs []config.Job) bool {
	for _, r := range jobs {
		if !r.Enabled || !r.StartExited {
			continue
		}
		matched, err := a.matchesJob(ctx, docker, c, r)
		if err != nil {
			logWarnf("job match error (%s): %v", r.ID, err)
			continue
		}
		if matched {
			logInfof("monitor: exited start matched by job jobID=%s container=%s", r.ID, containerName(c))
			return true
		}
	}

	if cfg.StartExited && hasAnyFilters(cfg) {
		logDebugf("monitor: exited start checking global filters container=%s", containerName(c))
		return a.matchesFilters(ctx, docker, c, cfg)
	}

	return false
}

func (a *app) matchesFilters(ctx context.Context, docker dockerClient, c containertypes.Summary, cfg config.Config) bool {
	name := containerName(c)

	if cfg.ContainerNameFilter != "" {
		allowed := splitCSV(cfg.ContainerNameFilter)
		ok := false
		for _, x := range allowed {
			if strings.Contains(name, x) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if cfg.ContainerLabelFilter != "" {
		ok := false
		for k, v := range c.Labels {
			if strings.Contains(k+"="+v, cfg.ContainerLabelFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if cfg.ContainerEnvVarFilter != "" {
		inspected, err := docker.InspectContainer(ctx, c.ID)
		if err != nil {
			logWarnf("inspect failed for env filter on container %s: %v", name, err)
			return false
		}
		if !dockerhealth.ContainerHasEnvVar(inspected, cfg.ContainerEnvVarFilter) {
			return false
		}
	}

	return true
}

func (a *app) handleHealth(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	exhausted := make([]string, 0)
	for id, v := range a.retriesExhausted {
		if v {
			exhausted = append(exhausted, id)
		}
	}
	resp := map[string]any{
		"ok":                    true,
		"lastScan":              a.lastScan,
		"unhealthyCount":        len(a.unhealthy) + len(a.restarting),
		"exitedCount":           len(a.exited),
		"totalExitedCount":      a.totalExited,
		"action":                cfg.Action,
		"startExited":           cfg.StartExited,
		"retriesExhaustedIDs":   exhausted,
		"monitoringStartsAt":    a.monitorStartsAt,
		"lastScanValid":         !a.lastScan.IsZero(),
		"encryptionKeyMismatch": crypto.HasDecryptionFailures(),
		"version":               appVersion,
		"latestVersion":         a.latestVersion,
		"latestVersionRelDate":  a.latestVersionRelDate,
		"bootTime":              a.bootTime,
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, resp)
}

func (a *app) handleDiagnostics(w http.ResponseWriter, _ *http.Request) {
	cfg := a.getConfig()
	ctx := context.Background()
	d := dockerhealth.Diagnostics{}
	_ = a.retryDocker(ctx, func() error {
		d = dockerhealth.SocketDiagnostics(ctx, a.docker, cfg.DockerSocketPath, cfg.DockerPingTimeoutSeconds)
		if d.DockerReachable {
			return nil
		}
		return errors.New(d.Details)
	})
	logDebugf("api: diagnostics requested mode=%s reachable=%t socketPresent=%t socketWritable=%t details=%q", d.Mode, d.DockerReachable, d.SocketPresent, d.SocketWritable, d.Details)
	writeJSON(w, http.StatusOK, d)
}

func (a *app) enrichContainerServiceMap(ctx context.Context, jobs []config.Job, cfg config.Config, serviceByName map[string]map[string]any) {
	hasEnabledJobs := false
	for _, job := range jobs {
		if job.Enabled {
			hasEnabledJobs = true
			break
		}
	}
	var listed []containertypes.Summary
	if err := a.retryDocker(ctx, func() error {
		var err error
		listed, err = a.docker.AllContainers(ctx)
		return err
	}); err != nil {
		return
	}

	type inspectResult struct {
		c        containertypes.Summary
		envKeys  []string
		envPairs []string
	}
	results := make([]inspectResult, len(listed))
	var wg sync.WaitGroup
	for i, c := range listed {
		wg.Add(1)
		go func(i int, c containertypes.Summary) {
			defer wg.Done()
			r := inspectResult{c: c}
			if inspected, err := a.docker.InspectContainer(ctx, c.ID); err == nil && inspected.Config != nil {
				r.envPairs = inspected.Config.Env
				r.envKeys = extractEnvKeys(r.envPairs)
			}
			results[i] = r
		}(i, c)
	}
	wg.Wait()

	for _, r := range results {
		c := r.c
		name := containerName(c)
		matchedJobs := matchedJobsSummary(name, c.Labels, r.envPairs, jobs)
		globalMatch := !hasEnabledJobs && hasAnyFilters(cfg) && matchesConfigFiltersSnapshot(name, c.Labels, r.envPairs, cfg)
		checkEligible := len(matchedJobs) > 0 || globalMatch
		if !checkEligible {
			continue
		}
		checkSource := "global"
		checkAction := cfg.Action
		if len(matchedJobs) > 0 {
			checkSource = "job"
			checkAction = strings.TrimSpace(fmt.Sprint(matchedJobs[0]["action"]))
		}
		serviceByName[name] = map[string]any{
			"id":            c.ID,
			"name":          name,
			"status":        c.Status,
			"state":         c.State,
			"labels":        c.Labels,
			"envKeys":       r.envKeys,
			"matchedJobs":   matchedJobs,
			"checkEligible": checkEligible,
			"checkSource":   checkSource,
			"checkAction":   checkAction,
		}
	}
}

func (a *app) handleContainers(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	containers := make([]map[string]any, 0, len(a.unhealthy))
	serviceByName := make(map[string]map[string]any, len(a.unhealthy)+len(a.exited))
	for _, c := range a.unhealthy {
		name := containerName(c)
		item := map[string]any{
			"id":            c.ID,
			"name":          name,
			"status":        c.Status,
			"state":         c.State,
			"labels":        c.Labels,
			"envKeys":       []string{},
			"matchedJobs":   []map[string]any{},
			"checkEligible": true,
			"checkSource":   "runtime",
			"checkAction":   "",
		}
		containers = append(containers, item)
		serviceByName[name] = item
	}
	exited := make([]map[string]any, 0, len(a.exited))
	for _, c := range a.exited {
		name := containerName(c)
		item := map[string]any{
			"id":            c.ID,
			"name":          name,
			"status":        c.Status,
			"state":         c.State,
			"labels":        c.Labels,
			"envKeys":       []string{},
			"matchedJobs":   []map[string]any{},
			"checkEligible": true,
			"checkSource":   "runtime",
			"checkAction":   "start",
		}
		exited = append(exited, item)
		serviceByName[name] = item
	}
	a.mu.RUnlock()

	cfg := a.getConfig()
	jobs := a.getJobs()
	if a.docker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		a.enrichContainerServiceMap(ctx, jobs, cfg, serviceByName)
	}

	all := make([]map[string]any, 0, len(serviceByName))
	for _, item := range serviceByName {
		all = append(all, item)
	}
	sort.Slice(all, func(i, j int) bool {
		left := strings.ToLower(fmt.Sprint(all[i]["name"]))
		right := strings.ToLower(fmt.Sprint(all[j]["name"]))
		return left < right
	})

	writeJSON(w, http.StatusOK, map[string]any{"unhealthy": containers, "exited": exited, "all": all})
}

func extractEnvKeys(envPairs []string) []string {
	if len(envPairs) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(envPairs))
	seen := make(map[string]struct{}, len(envPairs))
	for _, pair := range envPairs {
		key, _, _ := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func matchedJobsSummary(name string, labels map[string]string, envPairs []string, jobs []config.Job) []map[string]any {
	matches := make([]map[string]any, 0)
	for _, r := range jobs {
		if !r.Enabled {
			continue
		}
		if !jobMatchesServiceSnapshot(name, labels, envPairs, r) {
			continue
		}
		matches = append(matches, map[string]any{
			"id":     r.ID,
			"name":   r.Name,
			"group":  r.Group,
			"action": r.Action,
		})
	}
	return matches
}

func jobMatchesServiceSnapshot(name string, labels map[string]string, envPairs []string, r config.Job) bool {
	if r.ContainerNameFilter != "" {
		allowed := splitCSV(r.ContainerNameFilter)
		ok := false
		for _, x := range allowed {
			if strings.Contains(name, x) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if r.ContainerLabelFilter != "" {
		ok := false
		for k, v := range labels {
			if strings.Contains(k+"="+v, r.ContainerLabelFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if r.ContainerEnvVarFilter != "" {
		ok := false
		for _, envPair := range envPairs {
			if strings.Contains(envPair, r.ContainerEnvVarFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	return true
}

func matchesConfigFiltersSnapshot(name string, labels map[string]string, envPairs []string, cfg config.Config) bool {
	if cfg.ContainerNameFilter != "" {
		allowed := splitCSV(cfg.ContainerNameFilter)
		ok := false
		for _, x := range allowed {
			if strings.Contains(name, x) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if cfg.ContainerLabelFilter != "" {
		ok := false
		for k, v := range labels {
			if strings.Contains(k+"="+v, cfg.ContainerLabelFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if cfg.ContainerEnvVarFilter != "" {
		ok := false
		for _, envPair := range envPairs {
			if strings.Contains(envPair, cfg.ContainerEnvVarFilter) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	return true
}

func normalizeSettingsBody(body *config.FileConfig) {
	body.Action = strings.ToLower(strings.TrimSpace(body.Action))
	if body.Action == "" {
		body.Action = "restart"
	}
	if body.WorkerCount < 1 {
		body.WorkerCount = 2
	}
	if body.QueueSize < 1 {
		body.QueueSize = 64
	}
	if body.RetryCount < 1 {
		body.RetryCount = 1
	}
	if body.HttpClientTimeoutSeconds < 1 {
		body.HttpClientTimeoutSeconds = 15
	}
	if body.HttpMaxIdleConns < 1 {
		body.HttpMaxIdleConns = 20
	}
	if body.HttpMaxIdleConnsPerHost < 1 {
		body.HttpMaxIdleConnsPerHost = 10
	}
	if body.HttpIdleConnTimeoutSeconds < 1 {
		body.HttpIdleConnTimeoutSeconds = 90
	}
	if body.DockerPingTimeoutSeconds < 1 {
		body.DockerPingTimeoutSeconds = 5
	}
	if body.DockerClientRetryCount < 1 {
		body.DockerClientRetryCount = 1
	}
	if body.DockerClientRetryDelaySeconds < 1 {
		body.DockerClientRetryDelaySeconds = 2
	}
	if body.NotificationRatePerSec < 1 {
		body.NotificationRatePerSec = 5
	}
	if body.NotificationRatePerSec > 1000 {
		body.NotificationRatePerSec = 1000
	}
	if body.ActionTimeoutSeconds < 1 {
		body.ActionTimeoutSeconds = 20
	}
	if body.LogRetentionDays < 1 {
		body.LogRetentionDays = 7
	}
	if body.SQLiteBusyTimeoutSeconds < 1 {
		body.SQLiteBusyTimeoutSeconds = 5000
	}
	body.DockerSocketPath = strings.TrimSpace(body.DockerSocketPath)
	if body.DockerSocketPath == "" {
		body.DockerSocketPath = "/var/run/docker.sock"
	}
	switch body.Theme {
	case "light", "dark", "auto":
	default:
		body.Theme = "auto"
	}
	switch body.DashboardLayout {
	case "cards", "table", "grid":
	default:
		body.DashboardLayout = "cards"
	}
}

func syncConfigFromBody(cfg *config.Config, body config.FileConfig) {
	cfg.RetryCount = body.RetryCount
	cfg.RetryDelay = time.Duration(body.RetryDelaySeconds) * time.Second
	cfg.Action = body.Action
	cfg.WorkerCount = body.WorkerCount
	cfg.QueueSize = body.QueueSize
	cfg.HttpClientTimeoutSeconds = body.HttpClientTimeoutSeconds
	cfg.HttpMaxIdleConns = body.HttpMaxIdleConns
	cfg.HttpMaxIdleConnsPerHost = body.HttpMaxIdleConnsPerHost
	cfg.HttpIdleConnTimeoutSeconds = body.HttpIdleConnTimeoutSeconds
	cfg.DockerPingTimeoutSeconds = body.DockerPingTimeoutSeconds
	cfg.DockerClientRetryCount = body.DockerClientRetryCount
	cfg.DockerClientRetryDelaySeconds = body.DockerClientRetryDelaySeconds
	cfg.DockerSocketPath = body.DockerSocketPath
	cfg.ExternalHostname = body.ExternalHostname
	cfg.NotificationRatePerSec = body.NotificationRatePerSec
	cfg.NotificationCooldownSeconds = body.NotificationCooldownSeconds
	cfg.StartupDelaySeconds = body.StartupDelaySeconds
	cfg.StartExited = body.StartExited
	cfg.RunScriptPath = body.RunScriptPath
	cfg.BootNotification = body.BootNotification
	cfg.BootNotificationProfiles = body.BootNotificationProfiles
	cfg.ActionTimeoutSeconds = body.ActionTimeoutSeconds
	cfg.RequireFilterForExited = body.RequireFilterForExited
	cfg.SQLitePath = body.SQLitePath
	cfg.SQLiteBusyTimeoutSeconds = body.SQLiteBusyTimeoutSeconds
	cfg.SQLitePragmas = body.SQLitePragmas
	cfg.DisplayTimezone = body.DisplayTimezone
	cfg.ServerTimezone = body.ServerTimezone
	cfg.PrimaryBaseURL = body.PrimaryBaseURL
	cfg.LogLevel = body.LogLevel
	cfg.LogToFile = body.LogToFile
	cfg.LogRetentionDays = body.LogRetentionDays
	cfg.DashboardLayout = body.DashboardLayout
	cfg.DashboardRefreshSeconds = body.DashboardRefreshSeconds
	cfg.Theme = body.Theme
}

func (a *app) handleSettingsPUT(w http.ResponseWriter, r *http.Request) {
	var body config.FileConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	normalizeSettingsBody(&body)

	cfg := a.getConfig()
	logInfof("api: settings update requested action=%s workerCount=%d queueSize=%d", body.Action, body.WorkerCount, body.QueueSize)
	if err := config.SaveFileConfig(cfg.ConfigDir, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	reloaded, err := config.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	syncConfigFromBody(&reloaded, body)
	poolConfigChanged := reloaded.WorkerCount != cfg.WorkerCount || reloaded.QueueSize != cfg.QueueSize

	a.mu.Lock()
	a.cfg = reloaded
	a.httpClient.Timeout = time.Duration(reloaded.HttpClientTimeoutSeconds) * time.Second
	if transport, ok := a.httpClient.Transport.(*http.Transport); ok {
		transport.MaxIdleConns = reloaded.HttpMaxIdleConns
		transport.MaxIdleConnsPerHost = reloaded.HttpMaxIdleConnsPerHost
		transport.IdleConnTimeout = time.Duration(reloaded.HttpIdleConnTimeoutSeconds) * time.Second
	}
	a.notifier = notify.New(buildNotifierServicesFromProfiles(a.notifications))
	a.mu.Unlock()

	applyLogConfig(filepath.Join(reloaded.ConfigDir, "logs"), reloaded)

	if poolConfigChanged {
		logInfof("api: settings applying worker pool reload oldWorkerCount=%d oldQueueSize=%d newWorkerCount=%d newQueueSize=%d", cfg.WorkerCount, cfg.QueueSize, reloaded.WorkerCount, reloaded.QueueSize)
		a.replacePool(reloaded.WorkerCount, reloaded.QueueSize)
	}
	logInfof("api: settings update applied action=%s workerCount=%d queueSize=%d httpTimeoutSeconds=%d dockerPingTimeoutSeconds=%d dockerSocketPath=%s", reloaded.Action, reloaded.WorkerCount, reloaded.QueueSize, reloaded.HttpClientTimeoutSeconds, reloaded.DockerPingTimeoutSeconds, reloaded.DockerSocketPath)

	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "restartRecommended": true})
}

func (a *app) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.getConfig()
		fileCfg, err := config.LoadFileConfig(cfg.ConfigDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, fileCfg)
	case http.MethodPut:
		a.handleSettingsPUT(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleSettingsTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	if body.Theme != "auto" && body.Theme != "light" && body.Theme != "dark" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "theme must be auto, light, or dark"})
		return
	}
	cfg := a.getConfig()
	fileCfg, err := config.LoadFileConfig(cfg.ConfigDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	fileCfg.Theme = body.Theme
	if err := config.SaveFileConfig(cfg.ConfigDir, fileCfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	a.mu.Lock()
	a.cfg.Theme = body.Theme
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func jobMatchesFilters(job config.Job, qStr, actionFilter, enabledFilter, groupFilter string) bool {
	if actionFilter != "" && strings.ToLower(job.Action) != actionFilter {
		return false
	}
	if enabledFilter == "true" && !job.Enabled {
		return false
	}
	if enabledFilter == "false" && job.Enabled {
		return false
	}
	if groupFilter != "" && strings.ToLower(strings.TrimSpace(job.Group)) != groupFilter {
		return false
	}
	if qStr != "" {
		haystack := strings.ToLower(strings.Join([]string{
			job.Name, job.Group, job.Action,
			job.ContainerNameFilter, job.ContainerLabelFilter, job.ContainerEnvVarFilter,
		}, " "))
		return strings.Contains(haystack, qStr)
	}
	return true
}

func filterJobsList(jobs []config.Job, q url.Values) []config.Job {
	qStr := strings.ToLower(strings.TrimSpace(q.Get("q")))
	actionFilter := strings.ToLower(strings.TrimSpace(q.Get("action")))
	enabledFilter := strings.ToLower(strings.TrimSpace(q.Get("enabled")))
	groupFilter := strings.ToLower(strings.TrimSpace(q.Get("group")))
	if qStr == "" && actionFilter == "" && enabledFilter == "" && groupFilter == "" {
		return jobs
	}
	filtered := make([]config.Job, 0, len(jobs))
	for _, job := range jobs {
		if jobMatchesFilters(job, qStr, actionFilter, enabledFilter, groupFilter) {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

func paginateJobsList(jobs []config.Job, q url.Values) []config.Job {
	limit := 0
	offset := 0
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("limit"))); err == nil && n > 0 {
		limit = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("offset"))); err == nil && n >= 0 {
		offset = n
	}
	if offset == 0 && limit == 0 {
		return jobs
	}
	if offset >= len(jobs) {
		return []config.Job{}
	}
	jobs = jobs[offset:]
	if limit > 0 && limit < len(jobs) {
		jobs = jobs[:limit]
	}
	return jobs
}

func (a *app) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jobs := filterJobsList(a.getJobs(), r.URL.Query())
		total := len(jobs)
		jobs = paginateJobsList(jobs, r.URL.Query())
		writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "total": total})
	case http.MethodPost:
		var in config.Job
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		jobs := a.getJobs()
		next, saved, err := config.UpsertJob(jobs, in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.persistJobs(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleNotifications(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles := a.getNotificationProfiles()
		// Enrich each profile with live usage counters so the UI can show
		// the progress bar without the server needing a separate endpoint.
		type enriched struct {
			config.NotificationProfile
			DailyUsage int `json:"dailyUsage"`
			BurstUsage int `json:"burstUsage"`
		}
		out := make([]enriched, len(profiles))
		a.notifUsageMu.Lock()
		for i, p := range profiles {
			e := enriched{NotificationProfile: p}
			if u, ok := a.notifUsage[p.ID]; ok {
				today := time.Now().UTC().Format("2006-01-02")
				if u.dayKey == today {
					e.DailyUsage = u.dayCount
				}
				if p.BurstWindowMinutes > 0 {
					bw := time.Duration(p.BurstWindowMinutes) * time.Minute
					bk := time.Now().UTC().Truncate(bw).Format(time.RFC3339)
					if u.burstKey == bk {
						e.BurstUsage = u.burstCount
					}
				}
			}
			out[i] = e
		}
		a.notifUsageMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
	case http.MethodPut:
		var body struct {
			Profiles []config.NotificationProfile `json:"profiles"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if err := a.persistNotificationProfiles(body.Profiles); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		profiles := a.getNotificationProfiles()
		svc := buildNotifierServicesFromProfiles(profiles)

		a.mu.Lock()
		a.cfg.AppriseServices = svc
		a.notifier = notify.New(svc)
		a.mu.Unlock()

		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "profiles": profiles})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleNotificationByID handles per-profile actions:
//
//	POST /api/notifications/{id}/suspend  body: {"until":"RFC3339 or 'midnight'|'month-end'"}
//	POST /api/notifications/{id}/resume
//	POST /api/notifications/{id}/dismiss-error
func (a *app) handleNotificationByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// parse  /api/notifications/{id}/{action}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/notifications/"), "/", 2)
	if len(parts) != 2 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid path"})
		return
	}
	profileID, action := parts[0], parts[1]

	switch action {
	case "suspend":
		var body struct {
			Until string `json:"until"` // RFC3339 OR "midnight" | "1h" | "24h" | "month-end"
			TZ    string `json:"tz"`    // browser-detected IANA zone (fallback when DisplayTimezone unset)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var until time.Time
		// Store the browser timezone for future auto-suspends, then resolve.
		a.recordClientTimezone(body.TZ)
		tz := a.resolveTimezone()
		logDebugf("notif: suspend until=%s tz=%s (settings=%s client=%s stored=%s)",
			body.Until, tz, a.getConfig().DisplayTimezone, body.TZ, a.clientTimezone)
		switch body.Until {
		case "", "midnight":
			until = nextMidnight(tz)
		case "month-end":
			until = endOfMonth(tz)
		default:
			// Generic duration patterns: Nm = minutes, Nh = hours, Nd = days, Nw = weeks
			// e.g. "6h", "2d", "3w", "45m"
			d, parseErr := parseSuspendDuration(body.Until)
			if parseErr == nil {
				until = time.Now().Add(d)
			} else {
				// Fall back to RFC3339 literal timestamp
				parsed, err := time.Parse(time.RFC3339, body.Until)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid until value — use Nm/Nh/Nd/Nw, 'midnight', 'month-end', or RFC3339"})
					return
				}
				until = parsed
			}
		}
		a.suspendProfile(profileID, until, "")
		writeJSON(w, http.StatusOK, map[string]any{"suspended": true, "until": until})

	case "resume":
		a.resumeProfile(profileID)
		writeJSON(w, http.StatusOK, map[string]any{"resumed": true})

	case "dismiss-error":
		// Clear the persisted rate-limit error without changing suspension state
		a.mu.Lock()
		profiles := make([]config.NotificationProfile, len(a.notifications))
		copy(profiles, a.notifications)
		for i, p := range profiles {
			if p.ID == profileID {
				profiles[i].LastRateLimitError = ""
				profiles[i].LastRateLimitAt = nil
				break
			}
		}
		a.notifications = profiles
		configDir := a.cfg.ConfigDir
		a.mu.Unlock()
		_ = config.SaveNotificationProfiles(configDir, profiles)
		writeJSON(w, http.StatusOK, map[string]any{"dismissed": true})

	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown action: " + action})
	}
}

func builtInDockerHostProfile(socketPath string) config.DockerHostProfile {
	return config.DockerHostProfile{
		ID:       "local",
		Name:     "Local Docker",
		Type:     "socket",
		Endpoint: strings.TrimSpace(socketPath),
		Enabled:  true,
		BuiltIn:  true,
	}
}

// handleSystemAlerts returns recent critical system events — failed recoveries,
// paused monitoring, and monitoring-started — independently of notification
// agents. These always show in the bell regardless of whether any agent is
// configured or enabled.
func (a *app) handleSystemAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	type SystemAlert struct {
		ID            string    `json:"id"`
		Type          string    `json:"type"`
		ContainerName string    `json:"containerName,omitempty"`
		Message       string    `json:"message"`
		Timestamp     time.Time `json:"timestamp"`
		Action        string    `json:"action,omitempty"`
	}

	var alerts []SystemAlert

	// Monitoring-started event — present once the monitor has run at least once.
	// Use bootTime as the timestamp so it is always earlier than any recovery
	// event (you can't have a recovery failure before monitoring started).
	a.mu.RLock()
	lastScan := a.lastScan
	boot := a.bootTime
	a.mu.RUnlock()
	if !lastScan.IsZero() {
		alerts = append(alerts, SystemAlert{
			ID:        "monitoring-started-" + boot.Format("20060102T150405"),
			Type:      "monitoring_started",
			Message:   "Monitoring is active",
			Timestamp: boot, // boot time, not last-scan — always oldest event
		})
	}

	// Pull recent failed recoveries + paused monitoring from history
	if a.history != nil {
		entries, _, err := a.history.ListPage(history.ListOptions{
			Limit:        50,
			OnlyStatuses: []string{"failed", "exhausted"},
		})
		if err == nil {
			seen := make(map[string]bool) // deduplicate by container name
			for _, e := range entries {
				key := e.ContainerName + ":" + e.Status
				if seen[key] {
					continue
				}
				seen[key] = true
				alertType := "failed_recovery"
				msg := "Recovery action failed"
				if e.Status == "exhausted" {
					alertType = "paused_monitoring"
					msg = "Monitoring paused — max retries reached"
				}
				if e.Error != "" {
					msg = e.Error
				}
				alerts = append(alerts, SystemAlert{
					ID:            fmt.Sprintf("%s-%s-%d", e.ContainerID, e.Status, e.Timestamp.Unix()),
					Type:          alertType,
					ContainerName: e.ContainerName,
					Message:       msg,
					Timestamp:     e.Timestamp,
					Action:        e.Action,
				})
			}
		}
	}

	// Append update-available alert when a newer version has been found.
	a.mu.RLock()
	lv := a.latestVersion
	lvd := a.latestVersionRelDate
	a.mu.RUnlock()
	if lv != "" && lv != appVersion {
		alerts = append(alerts, SystemAlert{
			ID:   "update-available-" + lv,
			Type: "update_available",
			Message: fmt.Sprintf("Chowkidar %s is available (current: %s)%s", lv, appVersion, func() string {
				if lvd != "" {
					return " — released " + lvd
				}
				return ""
			}()),
			Timestamp: time.Now(),
		})
	}

	// Filter out dismissed alerts before returning.
	a.dismissedMu.Lock()
	dismissed := a.dismissedAlerts
	a.dismissedMu.Unlock()
	visible := alerts[:0]
	for _, al := range alerts {
		if _, ok := dismissed[al.ID]; !ok {
			visible = append(visible, al)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": visible})
}

// handleSystemAlertsDismiss handles POST /api/system-alerts/dismiss.
// Body: {"ids":["id1","id2"]} — marks alerts as dismissed server-side.
func (a *app) handleSystemAlertsDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ids array required"})
		return
	}
	now := time.Now().UTC()
	a.dismissedMu.Lock()
	if a.dismissedAlerts == nil {
		a.dismissedAlerts = make(map[string]time.Time)
	}
	for _, id := range body.IDs {
		a.dismissedAlerts[id] = now
	}
	snapshot := make(map[string]time.Time, len(a.dismissedAlerts))
	maps.Copy(snapshot, a.dismissedAlerts)
	a.dismissedMu.Unlock()
	if err := saveDismissedAlerts(a.cfg.ConfigDir, snapshot); err != nil {
		logWarnf("dismissed alerts: save failed: %v", err)
	}
	logInfof("api: dismissed alerts count=%d", len(body.IDs))
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": len(body.IDs)})
}

// saveDismissedAlerts persists dismissed alert IDs to disk.
// Entries older than 30 days are pruned to prevent unbounded growth.
func saveDismissedAlerts(configDir string, m map[string]time.Time) error {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	pruned := make(map[string]string, len(m))
	for id, t := range m {
		if t.After(cutoff) {
			pruned[id] = t.Format(time.RFC3339)
		}
	}
	data, err := json.MarshalIndent(pruned, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, "data", "dismissed-alerts.json")
	return os.WriteFile(path, data, 0o644)
}

// loadDismissedAlerts reads the dismissed alerts file from disk.
func loadDismissedAlerts(configDir string) (map[string]time.Time, error) {
	path := filepath.Join(configDir, "data", "dismissed-alerts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]time.Time), nil
		}
		return nil, err
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	m := make(map[string]time.Time, len(raw))
	for id, ts := range raw {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			m[id] = t
		}
	}
	return m, nil
}

// checkForUpdates polls the GitHub releases API for a newer version and stores
// the result in a.latestVersion.  It runs in its own goroutine and checks once
// on startup and then once every 24 hours (daily).
func (a *app) checkForUpdates(stop <-chan struct{}) {
	doCheck := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesURL, nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", "chowkidar/"+appVersion)
		resp, err := a.httpClient.Do(req)
		if err != nil {
			logDebugf("update-check: request failed: %v", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			logDebugf("update-check: unexpected status %d", resp.StatusCode)
			return
		}
		var payload struct {
			TagName     string `json:"tag_name"`
			PublishedAt string `json:"published_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return
		}
		latest := strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")
		if latest == "" {
			return
		}
		relDate := ""
		if payload.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, payload.PublishedAt); err == nil {
				relDate = t.Format("2006-01-02")
			}
		}
		a.mu.Lock()
		a.latestVersion = latest
		a.latestVersionRelDate = relDate
		a.latestVersionChecked = time.Now()
		a.mu.Unlock()
		if latest != appVersion {
			logInfof("update-check: new version available latest=%s current=%s", latest, appVersion)
		} else {
			logDebugf("update-check: up to date version=%s", appVersion)
		}
	}

	doCheck()
	for {
		select {
		case <-stop:
			return
		case <-time.After(24 * time.Hour):
			doCheck()
		}
	}
}

// pruneHistoryLoop removes action-history entries older than LogRetentionDays
// once at startup and then once every 24 hours.  It honours the same retention
// window used for log files so operators only need to configure one value.
func (a *app) pruneHistoryLoop(stop <-chan struct{}) {
	doPrune := func() {
		days := a.getConfig().LogRetentionDays
		if days <= 0 || a.history == nil {
			return
		}
		removed, err := a.history.Prune(days)
		if err != nil {
			logWarnf("history-prune: error retentionDays=%d err=%v", days, err)
			return
		}
		if removed > 0 {
			logInfof("history-prune: removed=%d retentionDays=%d", removed, days)
		} else {
			logDebugf("history-prune: nothing to remove retentionDays=%d", days)
		}
	}

	doPrune()
	for {
		select {
		case <-stop:
			return
		case <-time.After(24 * time.Hour):
			doPrune()
		}
	}
}

func (a *app) handleDockerHostsGET(w http.ResponseWriter) {
	cfg := a.getConfig()
	profiles := a.getDockerHostProfiles()
	builtIn := builtInDockerHostProfile(cfg.DockerSocketPath)
	all := append([]config.DockerHostProfile{builtIn}, profiles...)
	activeID := "local"
	for _, p := range profiles {
		if p.Type == "socket" && strings.TrimSpace(p.Endpoint) == strings.TrimSpace(cfg.DockerSocketPath) {
			activeID = p.ID
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": all, "activeHostID": activeID})
}

func (a *app) handleDockerHostsPUT(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profiles     []config.DockerHostProfile `json:"profiles"`
		ActiveHostID string                     `json:"activeHostID"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	toSave := make([]config.DockerHostProfile, 0)
	for _, p := range body.Profiles {
		if !p.BuiltIn && p.ID != "local" {
			toSave = append(toSave, p)
		}
	}
	if err := a.persistDockerHostProfiles(toSave); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	activeHostID := strings.TrimSpace(body.ActiveHostID)
	if activeHostID != "" {
		for _, p := range a.getDockerHostProfiles() {
			if p.ID != activeHostID {
				continue
			}
			if p.Type == "socket" {
				cfg := a.getConfig()
				cfg.DockerSocketPath = strings.TrimSpace(p.Endpoint)
				// Load the current FileConfig so fields not present in Config
				// (e.g. dashboardRefreshSeconds) are preserved on save.
				fileCfg, ferr := config.LoadFileConfig(cfg.ConfigDir)
				if ferr != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": ferr.Error()})
					return
				}
				fileCfg.DockerSocketPath = cfg.DockerSocketPath
				if err := config.SaveFileConfig(cfg.ConfigDir, fileCfg); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
					return
				}
				a.mu.Lock()
				a.cfg = cfg
				a.mu.Unlock()
			}
			break
		}
	}

	cfg2 := a.getConfig()
	savedProfiles := append([]config.DockerHostProfile{builtInDockerHostProfile(cfg2.DockerSocketPath)}, a.getDockerHostProfiles()...)
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "profiles": savedProfiles, "activeHostID": activeHostID})
}

func (a *app) handleDockerHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleDockerHostsGET(w)
	case http.MethodPut:
		a.handleDockerHostsPUT(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleDockerHostsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := a.getConfig()
	profiles := a.getDockerHostProfiles()

	allHosts := make([]config.DockerHostProfile, 0, len(profiles)+1)
	allHosts = append(allHosts, config.DockerHostProfile{
		ID:       "local",
		Name:     "Local Docker",
		Type:     "socket",
		Endpoint: strings.TrimSpace(cfg.DockerSocketPath),
		Enabled:  true,
		BuiltIn:  true,
	})
	allHosts = append(allHosts, profiles...)

	type hostStatus struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Connected bool   `json:"connected"`
		Info      string `json:"info"`
		ErrMsg    string `json:"error,omitempty"`
	}

	results := make([]hostStatus, len(allHosts))
	var wg sync.WaitGroup
	for i, h := range allHosts {
		wg.Add(1)
		go func(idx int, host config.DockerHostProfile) {
			defer wg.Done()
			s := hostStatus{ID: host.ID, Name: host.Name}
			ok, info, err := dockerhealth.PingHost(r.Context(), host.Type, host.Endpoint, cfg.DockerPingTimeoutSeconds, nil)
			s.Connected = ok
			s.Info = info
			if err != nil {
				s.ErrMsg = err.Error()
			}
			results[idx] = s
		}(i, h)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{"hosts": results})
}

func (a *app) handleTestDockerHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Type          string `json:"type"`
		Endpoint      string `json:"endpoint"`
		TLSCACert     string `json:"tlsCACert"`
		TLSCert       string `json:"tlsCert"`
		TLSKey        string `json:"tlsKey"`
		TLSSkipVerify bool   `json:"tlsSkipVerify"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	hostType := strings.ToLower(strings.TrimSpace(body.Type))
	endpoint := strings.TrimSpace(body.Endpoint)
	if endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "endpoint is required"})
		return
	}
	var tlsCfg *dockerhealth.TLSConfig
	if body.TLSCACert != "" || body.TLSCert != "" || body.TLSKey != "" || body.TLSSkipVerify {
		tlsCfg = &dockerhealth.TLSConfig{
			CACert:     body.TLSCACert,
			Cert:       body.TLSCert,
			Key:        body.TLSKey,
			SkipVerify: body.TLSSkipVerify,
		}
	}
	cfg := a.getConfig()
	ok, info, err := dockerhealth.PingHost(r.Context(), hostType, endpoint, cfg.DockerPingTimeoutSeconds, tlsCfg)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "info": info})
}

func (a *app) handleScripts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"scripts": a.getScriptEntries()})
	case http.MethodPut:
		var body struct {
			Scripts []config.ScriptEntry `json:"scripts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if err := a.persistScriptEntries(body.Scripts); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "scripts": a.getScriptEntries()})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// ensureDryRunContainer returns a real running container called chowkidar-dry-run.
// It creates one via the docker CLI if it doesn't exist yet, so that scripts
// running in dry-run mode can use docker inspect, docker ps, etc. against a
// real container. Falls back to a dummy summary when Docker is unavailable.
func (a *app) ensureDryRunContainer(ctx context.Context) containertypes.Summary {
	const name = "chowkidar-dry-run"
	dummy := containertypes.Summary{ID: "dry-run-test", Names: []string{"/" + name}}

	if a.docker != nil {
		all, err := a.docker.AllContainers(ctx)
		if err == nil {
			for _, c := range all {
				for _, n := range c.Names {
					if strings.TrimPrefix(n, "/") == name {
						return c
					}
				}
			}
		}
	}

	// Container not found — create it via docker CLI (available in the image).
	out, err := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", name,
		"--label", "chowkidar.managed=true",
		"--health-cmd", "echo ok",
		"--health-interval", "30s",
		"alpine:3", "sh", "-c", "while true; do sleep 5; done",
	).CombinedOutput()
	if err != nil {
		// Container may already exist but be stopped — try starting it.
		if strings.Contains(string(out), "already in use") {
			_ = exec.CommandContext(ctx, "docker", "start", name).Run()
			// Re-query
			if a.docker != nil {
				if all, err2 := a.docker.AllContainers(ctx); err2 == nil {
					for _, c := range all {
						for _, n := range c.Names {
							if strings.TrimPrefix(n, "/") == name {
								return c
							}
						}
					}
				}
			}
		}
		logWarnf("dry-run: could not create %s container: %v — using dummy", name, err)
		return dummy
	}

	id := strings.TrimSpace(string(out))
	return containertypes.Summary{ID: id, Names: []string{"/" + name}, State: "running"}
}

// scheduleDryRunCleanup resets (or creates) a timer that removes the
// chowkidar-dry-run container after the given idle duration.
func (a *app) scheduleDryRunCleanup(after time.Duration) {
	a.dryRunCleanupMu.Lock()
	defer a.dryRunCleanupMu.Unlock()
	if a.dryRunCleanupTimer != nil {
		a.dryRunCleanupTimer.Reset(after)
		return
	}
	a.dryRunCleanupTimer = time.AfterFunc(after, func() {
		a.dryRunCleanupMu.Lock()
		a.dryRunCleanupTimer = nil
		a.dryRunCleanupMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "docker", "rm", "-f", "chowkidar-dry-run").CombinedOutput()
		if err != nil {
			logWarnf("dry-run cleanup: %v — %s", err, strings.TrimSpace(string(out)))
		} else {
			logInfof("dry-run cleanup: removed chowkidar-dry-run container")
		}
	})
}

func (a *app) handleScriptDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Script string `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Script) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "script is required"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	// Use a real dry-run container so docker commands (inspect, ps, etc.) work.
	// DRY_RUN=1 is still injected so templates with a guard exit early safely.
	target := a.ensureDryRunContainer(ctx)
	output, err := a.executeRunScript(ctx, body.Script, a.getConfig(), target, containerName(target), true)

	// Schedule removal of the dry-run container 90 s after the last run.
	a.scheduleDryRunCleanup(90 * time.Second)

	resp := map[string]any{"output": output}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleScriptDryRunCleanup handles DELETE /api/scripts/dry-run.
// It immediately removes the chowkidar-dry-run container rather than waiting
// for the automatic 90-second idle timer.  Used by tests to clean up promptly.
func (a *app) handleScriptDryRunCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Cancel any pending timer so it doesn't interfere.
	a.cancelDryRunCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", "chowkidar-dry-run").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such container") {
			writeJSON(w, http.StatusOK, map[string]any{"removed": false, "reason": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": msg})
		return
	}
	logInfof("dry-run cleanup: removed chowkidar-dry-run container via API")
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (a *app) handleJobByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "job id is required"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		var in config.Job
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		in.ID = id

		jobs := a.getJobs()
		next, saved, err := config.UpsertJob(jobs, in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.persistJobs(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		jobs := a.getJobs()
		next, removed := config.DeleteJob(jobs, id)
		if !removed {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
			return
		}
		if err := a.persistJobs(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleHistoryEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleHistory(w, r)
	case http.MethodPost:
		// Accepts a JSON array of history entries for bulk insertion.
		// Used by the Playwright global setup to seed test data before the suite runs.
		if a.history == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "history store not available"})
			return
		}
		var entries []history.Entry
		if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
			return
		}
		for i := range entries {
			if err := a.history.Append(entries[i]); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}
		logInfof("api: history seeded count=%d", len(entries))
		writeJSON(w, http.StatusCreated, map[string]any{"seeded": len(entries)})
	case http.MethodDelete:
		if a.history == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "history store not available"})
			return
		}
		if err := a.history.Clear(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		logInfof("api: history cleared")
		writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	isBars := q.Get("bars") == "true"

	// Bars requests (sparklines) allow a higher limit so per-service history
	// is not crowded out by other services' events when many containers exist.
	maxLimit := 500
	if isBars {
		maxLimit = 2000
	}
	limit := 25
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= maxLimit {
			limit = parsed
		}
	}

	page := 1
	if raw := strings.TrimSpace(q.Get("page")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}

	offset := (page - 1) * limit

	service := strings.TrimSpace(q.Get("service"))

	// ?bars=true is used by the dashboard sparklines — include healthy pulse
	// events so bars turn green for containers with no action history.
	// The activity feed always excludes healthy to keep it readable.
	excludeStatus := "healthy"
	if isBars {
		excludeStatus = ""
	}
	entries, total, err := a.history.ListPage(history.ListOptions{
		Limit:         limit,
		Offset:        offset,
		Service:       service,
		ExcludeStatus: excludeStatus,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

func (a *app) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProfileID string `json:"profileId"`
		Service   string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}

	const testMsg = "This is a test notification from Chowkidar"

	cfg := a.getConfig()
	prefix := "[chowkidar]"
	if strings.TrimSpace(cfg.ExternalHostname) != "" {
		prefix = "[chowkidar@" + strings.TrimSpace(cfg.ExternalHostname) + "]"
	}

	if strings.TrimSpace(req.Service) != "" {
		svc := strings.TrimSpace(req.Service)
		logInfof("notify: test direct service=%s", svc)
		n := notify.New(svc)
		if err := n.Send(prefix+" test", testMsg); err != nil {
			logWarnf("notify: test failed service=%s err=%v", svc, err)
			writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": err.Error()})
			return
		}
		logInfof("notify: test ok service=%s", svc)
		writeJSON(w, http.StatusOK, map[string]any{"sent": true})
		return
	}

	if strings.TrimSpace(req.ProfileID) != "" {
		profiles := a.getNotificationProfiles()
		var found *config.NotificationProfile
		for i := range profiles {
			if profiles[i].ID == req.ProfileID {
				found = &profiles[i]
				break
			}
		}
		if found == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"sent": false, "error": "profile not found"})
			return
		}
		if strings.TrimSpace(found.Service) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": "profile has no service URL configured"})
			return
		}
		logInfof("notify: test profile=%s service=%s", found.ID, found.Service)
		n := notify.New(found.Service)
		if err := n.Send(prefix+" test", testMsg); err != nil {
			logWarnf("notify: test failed profile=%s err=%v", found.ID, err)
			writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": err.Error()})
			return
		}
		logInfof("notify: test ok profile=%s", found.ID)
		writeJSON(w, http.StatusOK, map[string]any{"sent": true})
		return
	}

	logInfof("notify: global test")
	if err := a.sendNotification("test", testMsg); err != nil {
		logWarnf("notify: global test failed err=%v", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"sent": false, "error": err.Error()})
		return
	}
	logInfof("notify: global test ok")
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func (a *app) handleManualAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Container string `json:"container"`
		Name      string `json:"name"`
		Action    string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	req.Container = strings.TrimSpace(req.Container)
	if req.Container == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "container is required"})
		return
	}

	resolvedName := strings.TrimSpace(req.Name)
	if resolvedName == "" {
		resolvedName = a.containerNameByID(req.Container)
	}
	c := containertypes.Summary{ID: req.Container, Names: []string{"/" + resolvedName}}
	reason := "manual " + req.Action
	logInfof("api: manual action queued container=%s action=%s", resolvedName, req.Action)
	a.enqueueJob(actionJob{app: a, container: c, reason: reason, action: req.Action, force: true})
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (a *app) enqueueJob(job worker.Job) {
	a.mu.RLock()
	pool := a.pool
	a.mu.RUnlock()
	if pool == nil {
		logWarnf("worker: enqueue skipped reason=pool-not-available")
		return
	}
	pool.Enqueue(job)
}

func (a *app) replacePool(workerCount, queueSize int) {
	newPool := worker.NewPool(workerCount, queueSize)
	a.mu.Lock()
	oldPool := a.pool
	a.pool = newPool
	a.mu.Unlock()
	logInfof("worker: pool replaced workerCount=%d queueSize=%d", workerCount, queueSize)
	if oldPool != nil {
		oldPool.Stop()
		logInfof("worker: previous pool stopped")
	}
}

func (a *app) stopPool() {
	a.mu.Lock()
	pool := a.pool
	a.pool = nil
	a.mu.Unlock()
	if pool != nil {
		pool.Stop()
		logInfof("worker: pool stopped")
	}
}

func (a *app) getConfig() config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

// resolveTimezone returns the best available IANA timezone for suspension
// calculations, using this priority order:
//  1. Settings DisplayTimezone (explicit admin/user choice)
//  2. clientTimezone (last timezone seen from a browser request)
//  3. Empty string → nextMidnight falls back to UTC
func (a *app) resolveTimezone() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg.DisplayTimezone != "" {
		return a.cfg.DisplayTimezone
	}
	return a.clientTimezone
}

// recordClientTimezone saves the browser-reported timezone so auto-suspend
// can use it when DisplayTimezone is not explicitly configured.
func (a *app) recordClientTimezone(tz string) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return
	}
	// Validate it's a real IANA zone before storing.
	if _, err := time.LoadLocation(tz); err != nil {
		logDebugf("notif: ignoring invalid client timezone=%q: %v", tz, err)
		return
	}
	a.mu.Lock()
	if a.clientTimezone != tz {
		logDebugf("notif: client timezone updated=%q", tz)
		a.clientTimezone = tz
	}
	a.mu.Unlock()
}

func (a *app) getJobs() []config.Job {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]config.Job, len(a.jobs))
	copy(out, a.jobs)
	return out
}

func (a *app) getNotificationProfiles() []config.NotificationProfile {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]config.NotificationProfile, len(a.notifications))
	copy(out, a.notifications)
	return out
}

func (a *app) getScriptEntries() []config.ScriptEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]config.ScriptEntry, len(a.scripts))
	copy(out, a.scripts)
	return out
}

func (a *app) getDockerHostProfiles() []config.DockerHostProfile {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]config.DockerHostProfile, len(a.dockerHosts))
	copy(out, a.dockerHosts)
	return out
}

// buildExtraClients creates Docker clients for all enabled non-built-in host profiles
// and stores them in a.extraClients. Closes any existing extra clients first.
func (a *app) buildExtraClients() {
	profiles := a.getDockerHostProfiles()
	var entries []*hostEntry
	for _, p := range profiles {
		if !p.Enabled || p.BuiltIn {
			continue
		}
		var tlsCfg *dockerhealth.TLSConfig
		if p.TLSCACert != "" || p.TLSCert != "" || p.TLSKey != "" || p.TLSSkipVerify {
			tlsCfg = &dockerhealth.TLSConfig{
				CACert:     p.TLSCACert,
				Cert:       p.TLSCert,
				Key:        p.TLSKey,
				SkipVerify: p.TLSSkipVerify,
			}
		}
		c, err := dockerhealth.NewClientForHost(p.Type, p.Endpoint, tlsCfg)
		if err != nil {
			logWarnf("docker-hosts: failed to create client for host %q: %v", p.Name, err)
			continue
		}
		entries = append(entries, &hostEntry{profile: p, client: c})
		logInfof("docker-hosts: client created host=%s id=%s", p.Name, p.ID)
	}
	a.mu.Lock()
	old := a.extraClients
	a.extraClients = entries
	a.mu.Unlock()
	for _, e := range old {
		_ = e.client.Close()
	}
}

func (a *app) getExtraClients() []*hostEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*hostEntry, len(a.extraClients))
	copy(out, a.extraClients)
	return out
}

func (a *app) persistJobs(jobs []config.Job) error {
	cfg := a.getConfig()
	if err := config.SaveJobs(cfg.ConfigDir, jobs); err != nil {
		return err
	}
	a.mu.Lock()
	a.jobs = jobs
	a.mu.Unlock()
	return nil
}

func (a *app) persistNotificationProfiles(profiles []config.NotificationProfile) error {
	cfg := a.getConfig()
	if err := config.SaveNotificationProfiles(cfg.ConfigDir, profiles); err != nil {
		return err
	}
	a.mu.Lock()
	a.notifications = profiles
	a.mu.Unlock()
	return nil
}

func (a *app) persistScriptEntries(scripts []config.ScriptEntry) error {
	cfg := a.getConfig()
	if err := config.SaveScriptEntries(cfg.ConfigDir, scripts); err != nil {
		return err
	}
	a.mu.Lock()
	a.scripts = scripts
	a.mu.Unlock()
	return nil
}

func (a *app) persistDockerHostProfiles(profiles []config.DockerHostProfile) error {
	cfg := a.getConfig()
	if err := config.SaveDockerHostProfiles(cfg.ConfigDir, profiles); err != nil {
		return err
	}
	a.mu.Lock()
	a.dockerHosts = profiles
	a.mu.Unlock()
	go a.buildExtraClients()
	return nil
}

func (a *app) logActionHistory(c containertypes.Summary, reason, action string, attempt int, status, errMsg, output string, duration time.Duration) {
	if a.history == nil {
		return
	}
	entry := history.Entry{
		Timestamp:     time.Now().UTC(),
		ContainerID:   c.ID,
		ContainerName: containerName(c),
		Reason:        reason,
		Action:        action,
		Attempt:       attempt,
		Status:        status,
		Error:         errMsg,
		Output:        strings.TrimSpace(output),
		DurationMs:    duration.Milliseconds(),
	}
	if err := a.history.Append(entry); err != nil {
		logErrorf("failed to append action history: %v", err)
	}
}

func (a *app) incrementActionCycle(containerID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actionCycle[containerID]++
	return a.actionCycle[containerID]
}

func (a *app) isRetriesExhausted(containerID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.retriesExhausted[containerID]
}

func (a *app) shouldNotify(containerID string) bool {
	cfg := a.getConfig()
	if cfg.NotificationCooldownSeconds <= 0 {
		return true
	}
	cooldown := time.Duration(cfg.NotificationCooldownSeconds) * time.Second

	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if last, ok := a.lastNotified[containerID]; ok {
		elapsed := now.Sub(last)
		if elapsed < cooldown {
			remaining := (cooldown - elapsed).Round(time.Second)
			logInfof("cooldown: notification suppressed id=%.12s elapsed=%s cooldown=%s remaining=%s",
				containerID, elapsed.Round(time.Second), cooldown, remaining)
			return false
		}
	}
	a.lastNotified[containerID] = now
	return true
}

func (a *app) setRetriesExhausted(containerID string, v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.retriesExhausted[containerID] = v
}

func (a *app) claimActiveJob(containerID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeJobs[containerID] {
		return false
	}
	a.activeJobs[containerID] = true
	return true
}

func (a *app) releaseActiveJob(containerID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.activeJobs, containerID)
}

func (a *app) setPostActionDeadline(containerID string, deadline time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.postActionDeadline[containerID] = deadline
}

func (a *app) postActionWaitRemaining(containerID string) time.Duration {
	a.mu.RLock()
	deadline, ok := a.postActionDeadline[containerID]
	a.mu.RUnlock()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		a.mu.Lock()
		delete(a.postActionDeadline, containerID)
		a.mu.Unlock()
		return 0
	}
	return remaining
}

func (a *app) resetActionCooldown(containerID string) {
	a.mu.Lock()
	delete(a.lastNotified, containerID)
	delete(a.actionCycle, containerID)
	delete(a.retriesExhausted, containerID)
	delete(a.postActionDeadline, containerID)
	delete(a.activeJobs, containerID)
	delete(a.lastJobRuleName, containerID)
	a.mu.Unlock()
	logInfof("cooldown: reset containerID=%.12s", containerID)
}

func (a *app) containerNameByID(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, c := range a.unhealthy {
		if c.ID == id {
			return containerName(c)
		}
	}
	for _, c := range a.exited {
		if c.ID == id {
			return containerName(c)
		}
	}
	if name, ok := a.knownNames[id]; ok {
		return name
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (a *app) handleResetCooldown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/containers/")
	id = strings.TrimSuffix(id, "/reset-cooldown")
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "container id is required"})
		return
	}
	name := a.containerNameByID(id)
	a.resetActionCooldown(name)
	logInfof("api: resume monitoring requested container=%s id=%.12s", name, id)
	a.logActionHistory(
		containertypes.Summary{ID: id, Names: []string{"/" + name}},
		"resume monitoring", "resume", 0, "success", "", "", 0,
	)
	writeJSON(w, http.StatusOK, map[string]any{"reset": true})
}

// allowedInterpreters is the set of shell interpreters permitted in script shebangs.
// Only these paths may be used as the exec.Command executable; anything else falls
// back to /bin/sh so a crafted shebang cannot invoke arbitrary binaries.
var allowedInterpreters = map[string]bool{
	"/bin/sh":             true,
	"/bin/bash":           true,
	"/usr/bin/sh":         true,
	"/usr/bin/bash":       true,
	"/usr/local/bin/bash": true,
	"/usr/local/bin/sh":   true,
}

// resolveScriptInterpreter reads the shebang line from a script and returns
// the interpreter path if it exists on the system and is in the allowlist,
// falling back to /bin/sh otherwise.
// This prevents "no such file or directory" when e.g. bash is not installed,
// and prevents a crafted shebang from executing arbitrary binaries.
func resolveScriptInterpreter(script string) string {
	const fallback = "/bin/sh"
	trimmed := strings.TrimSpace(script)
	if !strings.HasPrefix(trimmed, "#!") {
		return fallback
	}
	firstLine := strings.SplitN(trimmed, "\n", 2)[0]
	interpreter := strings.TrimSpace(strings.TrimPrefix(firstLine, "#!"))
	// Handle cases like "#!/usr/bin/env bash" — just take the first word
	parts := strings.Fields(interpreter)
	if len(parts) == 0 {
		return fallback
	}
	// If the shebang is "/usr/bin/env bash", resolve the named shell via LookPath
	candidate := parts[0]
	if candidate == "/usr/bin/env" && len(parts) > 1 {
		shellName := parts[1]
		// Only allow env-resolved shells that are in our allowlist by name
		if shellName != "bash" && shellName != "sh" {
			return fallback
		}
		if resolved, err := exec.LookPath(shellName); err == nil {
			if allowedInterpreters[resolved] {
				return resolved
			}
		}
		return fallback
	}
	// Direct path: must be in the allowlist and actually exist
	if allowedInterpreters[candidate] {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return fallback
}

// executeRunScript runs the inline or path-based script and returns its
// combined stdout+stderr output alongside any error.
func (a *app) executeRunScript(ctx context.Context, script string, cfg config.Config, c containertypes.Summary, name string, dryRun bool) (string, error) {
	if strings.TrimSpace(script) != "" {
		tmpFile, err := os.CreateTemp("", "chowkidar-*.sh")
		if err != nil {
			return "", fmt.Errorf("create temp script: %w", err)
		}
		tmpPath := tmpFile.Name()
		defer func() { _ = os.Remove(tmpPath) }()
		if _, err := tmpFile.WriteString(script); err != nil {
			_ = tmpFile.Close()
			return "", fmt.Errorf("write temp script: %w", err)
		}
		_ = tmpFile.Close()
		// Make the script executable so the kernel honours its shebang line
		// (e.g. #!/bin/bash) and invokes the correct interpreter directly.
		// This means we never pass a user-derived value as the command to exec —
		// tmpPath is created by the server and is not user-controlled.
		if err := os.Chmod(tmpPath, 0o500); err != nil {
			return "", fmt.Errorf("chmod temp script: %w", err)
		}
		cmd := exec.CommandContext(ctx, tmpPath, c.ID, name) // #nosec G204 — tmpPath is server-generated
		// Inherit environment and inject DRY_RUN when this is a test run from the
		// UI so scripts can skip destructive steps safely.
		cmd.Env = append(os.Environ(), "CHOWKIDAR=1")
		if dryRun || c.ID == "dry-run-test" {
			cmd.Env = append(cmd.Env, "DRY_RUN=1")
		}
		out, err := cmd.CombinedOutput()
		output := strings.TrimSpace(string(out))
		if err != nil {
			return output, fmt.Errorf("script failed: %w", err)
		}
		return output, nil
	}
	if strings.TrimSpace(cfg.RunScriptPath) == "" {
		return "", fmt.Errorf("run-script action selected but RUN_SCRIPT_PATH is empty")
	}
	if !a.isScriptAllowed(cfg.RunScriptPath) {
		return "", fmt.Errorf("run-script path is not in scripts allowlist")
	}
	cmd := exec.CommandContext(ctx, cfg.RunScriptPath, c.ID, name)
	out2, err2 := cmd.CombinedOutput()
	output2 := strings.TrimSpace(string(out2))
	if err2 != nil {
		return output2, fmt.Errorf("script failed: %w", err2)
	}
	return output2, nil
}

// executeAction runs the action for a container and returns any script output
// (non-empty only for run-script actions) alongside the error.
// scriptTimeoutSeconds overrides actionTimeoutSeconds specifically for run-script;
// pass 0 to use the job/global action timeout.
func (a *app) executeAction(ctx context.Context, docker dockerClient, action string, script string, c containertypes.Summary, actionTimeoutSeconds, scriptTimeoutSeconds int) (string, error) {
	cfg := a.getConfig()

	// Scripts can have a dedicated timeout that is independent of the docker action timeout.
	effectiveTimeout := func(override int) time.Duration {
		sec := override
		if sec < 1 {
			sec = actionTimeoutSeconds
		}
		if sec < 1 {
			sec = cfg.ActionTimeoutSeconds
		}
		return max(time.Duration(sec)*time.Second, 5*time.Second)
	}

	name := containerName(c)
	switch action {
	case "restart":
		actionCtx, cancel := context.WithTimeout(ctx, effectiveTimeout(0))
		defer cancel()
		return "", docker.RestartContainer(actionCtx, c.ID, 10)
	case "start":
		actionCtx, cancel := context.WithTimeout(ctx, effectiveTimeout(0))
		defer cancel()
		return "", docker.StartContainer(actionCtx, c.ID)
	case "stop":
		actionCtx, cancel := context.WithTimeout(ctx, effectiveTimeout(0))
		defer cancel()
		return "", docker.StopContainer(actionCtx, c.ID, 10)
	case "none":
		return "", nil
	case "run-script":
		scriptCtx, cancel := context.WithTimeout(ctx, effectiveTimeout(scriptTimeoutSeconds))
		defer cancel()
		return a.executeRunScript(scriptCtx, script, cfg, c, name, false)
	default:
		return "", fmt.Errorf("unsupported action %q", action)
	}
}

func (a *app) isScriptAllowed(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	entries := a.getScriptEntries()
	if len(entries) == 0 {
		return false
	}
	for _, s := range entries {
		if !s.Enabled {
			continue
		}
		if strings.TrimSpace(s.Path) == path {
			return true
		}
	}
	return false
}

// notifData holds the values available inside per-profile message templates.
type notifData struct {
	ContainerName    string
	Action           string
	Reason           string
	Cycle            int
	MaxRetries       int
	Cooldown         int
	RuleName         string
	BaseURL          string
	ExternalHostname string
}

// renderNotifTemplate executes a Go text/template with d as the data context,
// falling back to the raw template string on any error.
func renderNotifTemplate(tmpl string, d notifData) string {
	t, err := template.New("").Parse(tmpl)
	if err != nil {
		return tmpl
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return tmpl
	}
	return buf.String()
}

func (a *app) sendNotification(event, body string) error {
	if a.notifier == nil {
		return nil
	}
	cfg := a.getConfig()
	prefix := "[chowkidar]"
	bodyWithHost := body
	if strings.TrimSpace(cfg.ExternalHostname) != "" {
		host := strings.TrimSpace(cfg.ExternalHostname)
		prefix = "[chowkidar@" + host + "]"
		bodyWithHost = "host=" + host + " | " + body
	}
	logInfof("notify: send event=%s", event)
	if err := a.notifier.Send(prefix+" "+event, bodyWithHost); err != nil {
		logWarnf("notify: send failed event=%s err=%v", event, err)
		return err
	}
	return nil
}

// buildNotifierServicesFromProfiles returns the service CSV for the global notifier.
// The default-flagged profile (if any) is used exclusively; otherwise all enabled profiles.
func buildNotifierServicesFromProfiles(profiles []config.NotificationProfile) string {
	for _, p := range profiles {
		if p.IsDefault && strings.TrimSpace(p.Service) != "" {
			return strings.TrimSpace(p.Service)
		}
	}
	services := make([]string, 0, len(profiles))
	for _, p := range profiles {
		if p.Enabled && strings.TrimSpace(p.Service) != "" {
			services = append(services, strings.TrimSpace(p.Service))
		}
	}
	return strings.Join(services, ",")
}

// ── Notification rate limiting & suspension helpers ───────────────────────

// cleanNotifError strips the verbose Apprise Python log wrapper from an error
// message so what's stored (and shown in the bell) is readable.
//
// Raw Apprise error format:
//
//	apprise send failed: exit status 1 (2026-06-02 12:43:43,738 - WARNING - Failed to send ntfy ...)
//
// We want:                  Failed to send ntfy ...
func cleanNotifError(raw string) string {
	s := raw
	// Strip leading "apprise send failed: exit status N (" prefix
	if idx := strings.Index(s, " ("); idx >= 0 {
		s = s[idx+2:]
	}
	// Remove trailing ")" from the combined-output wrapper
	s = strings.TrimSuffix(strings.TrimSpace(s), ")")

	// The Apprise output is a Python log: "YYYY-MM-DD HH:MM:SS,mmm - LEVEL - Message"
	// Strip the "YYYY-MM-DD HH:MM:SS,mmm - LEVEL - " prefix.
	// We keep everything after the third " - ".
	parts := strings.SplitN(s, " - ", 3)
	if len(parts) == 3 {
		s = strings.TrimSpace(parts[2])
	}

	// Strip verbose help/upgrade phrases and URLs that appear after the meaningful error.
	// e.g. ntfy: "quota reached; increase your limits with a paid plan, see https://ntfy.sh/app"
	verbosePhrases := []string{
		"; increase your", "; upgrade your", "; consider upgrading",
		"; see https", "; visit https", " see https", ", see https",
		" https://", " http://",
	}
	lower := strings.ToLower(s)
	for _, sep := range verbosePhrases {
		if idx := strings.Index(lower, sep); idx >= 0 {
			s = strings.TrimRight(s[:idx], " ;,")
			lower = strings.ToLower(s)
		}
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return strings.TrimSpace(raw)
	}
	return s
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range []string{"quota", "rate limit", "too many", "429", "limit reached", "throttl", "42908", "daily message", "exceeded"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// nextMidnight returns midnight in the given IANA timezone, falling back to UTC.
// parseSuspendDuration parses strings like "6h", "30m", "3d", "2w" into a time.Duration.
func parseSuspendDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("too short")
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid number")
	}
	switch unit {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %q — use m/h/d/w", unit)
	}
}

func nextMidnight(ianaZone string) time.Time {
	loc, err := time.LoadLocation(ianaZone)
	if err != nil || ianaZone == "" {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day()+1, 0, 0, 0, 0, loc)
}

func endOfMonth(ianaZone string) time.Time {
	loc, err := time.LoadLocation(ianaZone)
	if err != nil || ianaZone == "" {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	first := time.Date(n.Year(), n.Month()+1, 1, 0, 0, 0, 0, loc)
	return first
}

func (a *app) isProfileSuspended(p config.NotificationProfile) bool {
	return p.SuspendedUntil != nil && p.SuspendedUntil.After(time.Now().UTC())
}

// getOrCreateUsage returns the usage bucket for a profile, resetting stale counters.
func (a *app) getOrCreateUsage(profileID string, p config.NotificationProfile) *notifProfileUsage {
	a.notifUsageMu.Lock()
	defer a.notifUsageMu.Unlock()
	u, ok := a.notifUsage[profileID]
	if !ok {
		u = &notifProfileUsage{}
		a.notifUsage[profileID] = u
	}
	today := time.Now().UTC().Format("2006-01-02")
	if u.dayKey != today {
		u.dayKey = today
		u.dayCount = 0
	}
	if p.BurstWindowMinutes > 0 {
		bw := time.Duration(p.BurstWindowMinutes) * time.Minute
		bucketKey := time.Now().UTC().Truncate(bw).Format(time.RFC3339)
		if u.burstKey != bucketKey {
			u.burstKey = bucketKey
			u.burstCount = 0
		}
	}
	return u
}

// suspendProfile writes SuspendedUntil + optional error to the profile and persists.
func (a *app) suspendProfile(profileID string, until time.Time, reason string) {
	a.mu.Lock()
	profiles := make([]config.NotificationProfile, len(a.notifications))
	copy(profiles, a.notifications)
	for i, p := range profiles {
		if p.ID != profileID {
			continue
		}
		t := until
		profiles[i].SuspendedUntil = &t
		if reason != "" {
			profiles[i].LastRateLimitError = reason
			now := time.Now().UTC()
			profiles[i].LastRateLimitAt = &now
		}
		break
	}
	a.notifications = profiles
	configDir := a.cfg.ConfigDir
	a.mu.Unlock()
	if err := config.SaveNotificationProfiles(configDir, profiles); err != nil {
		logWarnf("notif: failed to persist suspension for profile=%s: %v", profileID, err)
	}
}

// resumeProfile clears the suspension and persists.
func (a *app) resumeProfile(profileID string) {
	a.mu.Lock()
	profiles := make([]config.NotificationProfile, len(a.notifications))
	copy(profiles, a.notifications)
	for i, p := range profiles {
		if p.ID == profileID {
			profiles[i].SuspendedUntil = nil
			break
		}
	}
	a.notifications = profiles
	configDir := a.cfg.ConfigDir
	a.mu.Unlock()
	if err := config.SaveNotificationProfiles(configDir, profiles); err != nil {
		logWarnf("notif: failed to persist resume for profile=%s: %v", profileID, err)
	}
}

func (a *app) sendJobNotifications(event string, data notifData, profileIDs []string) error {
	if len(profileIDs) == 0 {
		return nil
	}
	profiles := a.getNotificationProfiles()
	byID := make(map[string]config.NotificationProfile, len(profiles))
	for _, p := range profiles {
		byID[p.ID] = p
	}
	cfg := a.getConfig()
	host := strings.TrimSpace(cfg.ExternalHostname)
	baseURL := strings.TrimSpace(cfg.PrimaryBaseURL)
	// Inject cfg-level values so templates can reference them even when
	// the call-site didn't populate them.
	if data.ExternalHostname == "" {
		data.ExternalHostname = host
	}
	if data.BaseURL == "" {
		data.BaseURL = baseURL
	}
	prefix := "[chowkidar]"
	if host != "" {
		prefix = "[chowkidar@" + host + "]"
	}
	var lastErr error
	for _, id := range profileIDs {
		p, ok := byID[id]
		if !ok || !p.Enabled || strings.TrimSpace(p.Service) == "" {
			continue
		}

		// ── Suspension check ──────────────────────────────────────────────
		if a.isProfileSuspended(p) {
			logInfof("notif: skip suspended profile=%s until=%s", p.Name, p.SuspendedUntil.Format(time.RFC3339))
			continue
		}

		// ── Rate limit checks (in-memory counters) ────────────────────────
		if p.DailyLimit > 0 || p.BurstLimit > 0 {
			u := a.getOrCreateUsage(id, p)
			a.notifUsageMu.Lock()
			hitBurst := p.BurstLimit > 0 && u.burstCount >= p.BurstLimit
			hitDaily := p.DailyLimit > 0 && u.dayCount >= p.DailyLimit
			a.notifUsageMu.Unlock()
			if hitBurst || hitDaily {
				reason := "daily limit reached"
				if hitBurst {
					reason = "burst limit reached"
				}
				logWarnf("notif: limit hit profile=%s reason=%s", p.Name, reason)
				action := p.OnLimitAction
				if action == "" {
					action = "auto-suspend"
				}
				switch action {
				case "auto-suspend":
					a.suspendProfile(id, nextMidnight(a.resolveTimezone()), reason)
				case "drop":
					// silently drop
				}
				lastErr = fmt.Errorf("notification limit hit: %s", reason)
				continue
			}
		}

		titleTmpl := p.AppliedTitleTemplate(event)
		var title string
		if titleTmpl != "" {
			title = prefix + " " + renderNotifTemplate(titleTmpl, data)
		} else {
			title = prefix + " " + event
		}
		body := renderNotifTemplate(p.AppliedTemplate(event), data)
		if host != "" {
			body = "host=" + host + " | " + body
		}
		n := notify.New(p.Service)
		if err := n.Send(title, body); err != nil {
			logWarnf("job notification failed profile=%s err=%v", p.Name, err)
			lastErr = err

			// ── Auto-suspend on quota / rate-limit error ───────────────
			if isRateLimitError(err) {
				shouldAutoSuspend := p.AutoSuspendOnError || p.OnLimitAction == "auto-suspend" || p.OnLimitAction == ""
				if shouldAutoSuspend && !a.isProfileSuspended(p) {
					tz := a.resolveTimezone()
					until := nextMidnight(tz)
					logInfof("notif: auto-suspending profile=%s until=%s tz=%s reason=rate-limit", p.Name, until.Format(time.RFC3339), tz)
					a.suspendProfile(id, until, cleanNotifError(err.Error()))
				}
			}
		} else {
			// Increment usage counters on successful send
			if p.DailyLimit > 0 || p.BurstLimit > 0 {
				u := a.getOrCreateUsage(id, p)
				a.notifUsageMu.Lock()
				u.dayCount++
				u.burstCount++
				a.notifUsageMu.Unlock()
			}
		}
	}
	return lastErr
}

func (a *app) isSelfContainer(c containertypes.Summary) bool {
	if a.selfID != "" && strings.HasPrefix(c.ID, a.selfID) {
		return true
	}
	if a.selfName != "" && containerName(c) == a.selfName {
		return true
	}
	return false
}

func detectSelfIdentity(a *app) (string, string) {
	raw, err := os.ReadFile("/etc/hostname")
	if err != nil {
		return "", ""
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return "", ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspected, err := a.docker.InspectContainer(ctx, id)
	if err != nil {
		return id, ""
	}
	return id, dockerhealth.ContainerName(inspected)
}

// migrateSecretsToEncrypted encrypts any plaintext secrets found in config and
// notification profiles on first boot after CHOWKIDAR_SECRET_KEY is set.
// Already-encrypted values are left untouched.
func migrateSecretsToEncrypted(configDir string, profiles []config.NotificationProfile, dockerHosts []config.DockerHostProfile) {
	// Migrate config.yaml — re-load, let SaveFileConfig encrypt any plaintext fields.
	fileCfg, err := config.LoadFileConfig(configDir)
	if err != nil {
		logWarnf("encrypt-migration: failed to load config: %v", err)
	} else if saveErr := config.SaveFileConfig(configDir, fileCfg); saveErr != nil {
		logWarnf("encrypt-migration: failed to save config: %v", saveErr)
	} else {
		logInfof("encrypt-migration: config.yaml secrets encrypted")
	}

	// Migrate notifications.yaml — re-save profiles, SaveNotificationProfiles encrypts each service URL.
	if saveErr := config.SaveNotificationProfiles(configDir, profiles); saveErr != nil {
		logWarnf("encrypt-migration: failed to save notification profiles: %v", saveErr)
	} else {
		logInfof("encrypt-migration: notifications.yaml secrets encrypted")
	}

	// Migrate docker_hosts.yaml — re-save profiles, SaveDockerHostProfiles encrypts TLS fields.
	if saveErr := config.SaveDockerHostProfiles(configDir, dockerHosts); saveErr != nil {
		logWarnf("encrypt-migration: failed to save docker host profiles: %v", saveErr)
	} else {
		logInfof("encrypt-migration: docker_hosts.yaml secrets encrypted")
	}
}

func ensureConfigFile(cfg config.Config) error {
	path := filepath.Join(cfg.ConfigDir, "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return config.SaveFileConfig(cfg.ConfigDir, config.ToFileConfig(cfg))
}

func hasAnyFilters(cfg config.Config) bool {
	return strings.TrimSpace(cfg.ContainerNameFilter) != "" || strings.TrimSpace(cfg.ContainerLabelFilter) != "" || strings.TrimSpace(cfg.ContainerEnvVarFilter) != ""
}

func hasAnyJobFilters(jobs []config.Job) bool {
	for _, r := range jobs {
		if strings.TrimSpace(r.ContainerNameFilter) != "" || strings.TrimSpace(r.ContainerLabelFilter) != "" || strings.TrimSpace(r.ContainerEnvVarFilter) != "" {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// isSecureRequest returns true when the connection is HTTPS, either directly
// or via a reverse proxy that sets X-Forwarded-Proto.
func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func containerName(c containertypes.Summary) string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// parseExitCode extracts the exit code from a Docker status string like "Exited (1) 2 minutes ago".
// Returns -1 if the string is not in that format.
func parseExitCode(status string) int {
	idx1 := strings.Index(status, "(")
	idx2 := strings.Index(status, ")")
	if idx1 < 0 || idx2 <= idx1 {
		return -1
	}
	code, err := strconv.Atoi(strings.TrimSpace(status[idx1+1 : idx2]))
	if err != nil {
		return -1
	}
	return code
}

// effectiveRetryCount returns the retry limit for a job, computing it from
// MaxMonitoringDurationMinutes when set (maxRetries = duration / scanInterval).
func effectiveRetryCount(r *config.Job) int {
	if r == nil {
		return 0
	}
	if r.MaxMonitoringDurationMinutes > 0 {
		interval := time.Duration(r.MonitorIntervalSeconds) * time.Second
		if interval < 5*time.Second {
			interval = 60 * time.Second
		}
		n := max(int(time.Duration(r.MaxMonitoringDurationMinutes)*time.Minute/interval), 1)
		return n
	}
	if r.RetryCount > 0 {
		return r.RetryCount
	}
	return 0
}

func splitCSV(in string) []string {
	parts := strings.Split(in, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ── Auth helpers ──────────────────────────────────────────────────────────────

const sessionCookieName = "chowkidar_session"

func (a *app) createSession(username string, remember bool) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	expiry := time.Now().Add(24 * time.Hour)
	if remember {
		expiry = time.Now().Add(30 * 24 * time.Hour)
	}
	a.sessionMu.Lock()
	a.sessions[token] = sessionEntry{username: username, expiresAt: expiry}
	a.sessionMu.Unlock()
	return token
}

func (a *app) validateSession(r *http.Request) (string, bool) {
	if a.authCfg == nil || !a.authCfg.Enabled {
		return "", true // auth disabled, always valid
	}
	// Setup required (no password set yet) — allow through so the setup API is reachable
	if a.authCfg.PasswordHash == "" {
		return "", true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	entry, ok := a.sessions[cookie.Value]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(a.sessions, cookie.Value)
		return "", false
	}
	return entry.username, true
}

func (a *app) deleteSession(r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return
	}
	a.sessionMu.Lock()
	delete(a.sessions, cookie.Value)
	a.sessionMu.Unlock()
}

func (a *app) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := a.validateSession(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// ── Auth handlers ─────────────────────────────────────────────────────────────

func (a *app) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Seed the client timezone from the tz query param sent on every page load.
	a.recordClientTimezone(r.URL.Query().Get("tz"))
	// setup_required = auth is enabled but no password has been set yet (first boot)
	setupRequired := a.authCfg == nil || (a.authCfg.Enabled && a.authCfg.PasswordHash == "")
	enabled := a.authCfg != nil && a.authCfg.Enabled
	username, loggedIn := a.validateSession(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       enabled,
		"loggedIn":      loggedIn,
		"username":      username,
		"setupRequired": setupRequired,
	})
}

func (a *app) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	if a.authCfg == nil || !a.authCfg.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "auth disabled"})
		return
	}
	if req.Username != a.authCfg.Username {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.authCfg.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	token := a.createSession(req.Username, req.Remember)
	maxAge := 86400
	if req.Remember {
		maxAge = 86400 * 30
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": req.Username})
}

func (a *app) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a.deleteSession(r)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		Username        string `json:"username"` // for first-time setup
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	if req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "new password required"})
		return
	}
	// First-time setup: no password set yet (PasswordHash empty = setup required)
	if a.authCfg == nil || !a.authCfg.Enabled || a.authCfg.PasswordHash == "" {
		if req.Username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username required for first-time setup"})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to hash password"})
			return
		}
		a.authCfg = &config.AuthConfig{Enabled: true, Username: req.Username, PasswordHash: string(hash)}
		if err := config.SaveAuthConfig(a.cfg.ConfigDir, a.authCfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to save auth config"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	// Change existing password
	if err := bcrypt.CompareHashAndPassword([]byte(a.authCfg.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "current password incorrect"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to hash password"})
		return
	}
	a.authCfg.PasswordHash = string(hash)
	if err := config.SaveAuthConfig(a.cfg.ConfigDir, a.authCfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to save auth config"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleAuthDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.authCfg != nil {
		a.authCfg.Enabled = false
		_ = config.SaveAuthConfig(a.cfg.ConfigDir, a.authCfg)
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.sessions = make(map[string]sessionEntry)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
