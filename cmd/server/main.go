package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

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

type app struct {
	cfg                    config.Config
	docker                 dockerClient
	extraClients           []*hostEntry
	notifier               notifierClient
	pool                   *worker.Pool
	diagnostics            dockerhealth.Diagnostics
	selfID                 string
	selfName               string
	lastNotified           map[string]time.Time
	cState                 map[string]*containerActionState // per-container action-cycle state
	activeJobs             map[string]bool
	lastJobScan            map[string]time.Time
	lastJobNotifications   map[string][]string
	lastJobRuleName        map[string]string
	knownNames             map[string]string
	httpClient             *http.Client
	jobs                   []config.Job
	notifications          []config.NotificationProfile
	notifUsage             map[string]*notifProfileUsage // profile ID → in-memory usage (reset on restart)
	notifUsageMu           sync.Mutex
	dockerHosts            []config.DockerHostProfile
	maintenanceWindows     []config.MaintenanceWindow
	scripts                []config.ScriptEntry
	history                *history.Store
	mu                     sync.RWMutex
	lastScan               time.Time
	monitorStartsAt        time.Time
	unhealthy              []containertypes.Summary
	exited                 []containertypes.Summary
	restarting             []containertypes.Summary
	totalExited            int
	authCfg                *config.AuthConfig
	sessions               map[string]sessionEntry
	sessionMu              sync.Mutex
	dryRunCleanupMu        sync.Mutex
	dryRunCleanupTimer     *time.Timer
	latestVersion          string               // non-empty when a newer release is found
	latestVersionChecked   time.Time            // when the check last ran
	latestVersionRelDate   string               // release date of latest version
	bootTime               time.Time            // set once at startup; used to key the monitoring-started alert
	clientTimezone         string               // last browser-reported IANA zone; reused when DisplayTimezone is unset
	dismissedAlerts        map[string]time.Time // alert ID → time dismissed; persisted to dismissed-alerts.json
	dismissedMu            sync.Mutex
	dockerHostConnected    map[string]bool
	dockerHostOfflineSince map[string]time.Time // when each host first went offline in the current outage
	dockerHostNotified     map[string]bool      // whether the down notification has been sent for the current outage
	dockerHostLastScan     map[string]time.Time
	maintenanceActiveIDs   map[string]string        // window ID → title, as of the previous scan cycle (title kept so "ended" alerts can name the window after it stops being active)
	maintenanceTransitions []maintenanceTransition  // recent start/end edges (pruned to ~24h), source of maintenance_started/maintenance_ended bell alerts
	activeJobCancels       map[string]activeJobCancel // container name → {jobID, cancel} for its in-flight action job (registered while running; used by force-cancel maintenance windows)
}

type dockerClient interface {
	UnhealthyContainers(ctx context.Context) ([]containertypes.Summary, error)
	ExitedContainers(ctx context.Context) ([]containertypes.Summary, error)
	RestartingContainers(ctx context.Context) ([]containertypes.Summary, error)
	StartingContainers(ctx context.Context) ([]containertypes.Summary, error)
	RunningContainers(ctx context.Context) ([]containertypes.Summary, error)
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
	postActionWaitSeconds int
}

// activeJobCancel pairs a running action job's cancel func with the job ID it
// was dispatched for (empty for job-less actions — manual restarts and
// start-exited recovery — which always run against the local Docker client).
// Registered in (*app).activeJobCancels for the duration of the job's Run, so
// a force-cancel maintenance window can find and interrupt exactly the
// in-flight jobs it now governs; see (*app).cancelForceCancelledJobs.
type activeJobCancel struct {
	jobID  string
	cancel context.CancelFunc
}

// appVersion is set at build time via -ldflags "-X main.appVersion=x.y.z".
// Falls back to "dev" when built without the flag (local development).
var appVersion = "dev"

type bootFiles struct {
	jobs                 []config.Job
	notificationProfiles []config.NotificationProfile
	dockerHostProfiles   []config.DockerHostProfile
	maintenanceWindows   []config.MaintenanceWindow
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

	maintenanceWindows, err := config.LoadMaintenanceWindows(cfg.ConfigDir)
	if err != nil {
		return f, fmt.Errorf("load maintenance windows: %w", err)
	}
	if err := config.SaveMaintenanceWindows(cfg.ConfigDir, maintenanceWindows); err != nil {
		return f, fmt.Errorf("init maintenance file: %w", err)
	}
	logInfof("boot: maintenance windows loaded count=%d path=%s", len(maintenanceWindows), filepath.Join(cfg.ConfigDir, "maintenance.yaml"))

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
	f.maintenanceWindows = maintenanceWindows
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
	// Log at the now-active level so the line is always visible.
	// Source: LOG_LEVEL env var takes priority over config.yaml logLevel.
	logInfof("boot: log config applied effectiveLogLevel=%s configLogLevel=%s logToFile=%t logRetentionDays=%d",
		logLevelLabel(configuredLogLevel), cfg.LogLevel, cfg.LogToFile, cfg.LogRetentionDays)
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
		cfg:                    cfg,
		docker:                 dockerClient,
		notifier:               notifier,
		pool:                   pool,
		jobs:                   files.jobs,
		notifications:          files.notificationProfiles,
		dockerHosts:            files.dockerHostProfiles,
		maintenanceWindows:     files.maintenanceWindows,
		scripts:                files.scriptEntries,
		history:                historyStore,
		lastNotified:           make(map[string]time.Time),
		cState:                 make(map[string]*containerActionState),
		activeJobs:             make(map[string]bool),
		lastJobScan:            make(map[string]time.Time),
		lastJobNotifications:   make(map[string][]string),
		notifUsage:             make(map[string]*notifProfileUsage),
		lastJobRuleName:        make(map[string]string),
		knownNames:             make(map[string]string),
		authCfg:                authCfg,
		sessions:               make(map[string]sessionEntry),
		bootTime:               time.Now().UTC(),
		dismissedAlerts:        make(map[string]time.Time),
		dockerHostConnected:    map[string]bool{},
		dockerHostOfflineSince: map[string]time.Time{},
		dockerHostNotified:     map[string]bool{},
		dockerHostLastScan:     map[string]time.Time{},
		activeJobCancels:       make(map[string]activeJobCancel),
	}
	defer a.stopPool()
	if da, err := loadDismissedAlerts(cfg.ConfigDir); err != nil {
		logWarnf("boot: could not load dismissed alerts: %v", err)
	} else {
		a.dismissedAlerts = da
		logInfof("boot: dismissed alerts loaded count=%d", len(da))
	}
	if nu, err := loadNotifUsage(cfg.ConfigDir); err != nil {
		logWarnf("boot: could not load notification usage counters: %v", err)
	} else {
		a.notifUsage = nu
		logInfof("boot: notification usage counters loaded count=%d", len(nu))
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
	go a.pruneFinishedMaintenanceLoop(stopMonitor)
	logInfof("boot: maintenance-window pruner started")

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
	mux.HandleFunc("/api/maintenance", a.authMiddleware(a.handleMaintenance))
	mux.HandleFunc("/api/maintenance/active", a.authMiddleware(a.handleMaintenanceActive))
	mux.HandleFunc("/api/maintenance/", a.authMiddleware(a.handleMaintenanceByID))
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

func (a *app) getMaintenanceWindows() []config.MaintenanceWindow {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]config.MaintenanceWindow, len(a.maintenanceWindows))
	copy(out, a.maintenanceWindows)
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
