package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"chowkidar/internal/config"
)

const githubReleasesURL = "https://api.github.com/repos/darkmatterforge/chowkidar/releases/latest"

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
