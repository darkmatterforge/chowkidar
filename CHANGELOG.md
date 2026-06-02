# Changelog

All notable changes to this project will be documented in this file.

## [0.2.0] - 2026-06-02

### Added
- Parallelized container inspection for dashboard API (faster UI loads)
- Loading spinners and button disables for Save/Job actions in UI
- E2E: Playwright tests for all major flows, including template upgrades, dry-run, and notification delivery
- Unit tests for notification limits and profile suspension logic

### Fixed
- Security: eliminate user-tainted value from exec.Command to fix CodeQL go/command-injection
- Backend: lock/unlock safety in finalizeState and handleAuthDisable
- Frontend: always check fetch .ok before .json() in settings/profile saves
- E2E: removed unnecessary waitForTimeout in health recovery spec
- Notification error logging now uses logWarnf
- Remove unused hostID field and countEnabledJobs function

### Changed
- Rename "Skip Cooldown & Retry" to "Restart Monitoring"
- Add jq to Docker images for script support
- Restrict script shebang interpreter to allowlist
- Improved frontend performance for dashboard rendering

## [0.1.1] - 2026-06-01

### Fixed
- Set `Secure` cookie attribute when the request is served over HTTPS
- Remove double `v` prefix in GitHub release names

### Changed
- Bump Alpine base image from 3.21 to 3.23
- Bump GitHub Actions: `actions/checkout` v4→v6, `actions/setup-go` v5→v6, `docker/login-action` v3→v4, `docker/metadata-action` v5→v6, `docker/setup-buildx-action` v3→v4

## [0.1.0] - 2026-06-01

### Added
- Initial public release of Chowkidar
- Multi-architecture Docker images (`linux/amd64` and `linux/arm64`)
- Container health monitoring — watches for `unhealthy` events and exited containers
- Configurable actions per job: `restart`, `start`, `stop`, `none`, `run-script`
- Per-job overrides for retry count, retry delay, monitoring interval and action timeout
- Jobs system with container name, label, and env var filters
- Multi-Docker-host support — monitor containers across multiple hosts or sockets
- Notification providers: Discord, Slack, Telegram, ntfy, Gotify, Pushover, SMTP/email, Webhook, raw Apprise URLs
- Per-provider notification templates for 7 lifecycle events (boot, unhealthy, retrying, cooldown, recovered, action failed, retry limit)
- Web UI with dashboard, jobs, notification profiles, action history and settings
- Three dashboard layouts: Card List, Compact Table, Status Grid
- Light / dark / auto theme with server-side persistence
- Optional username/password authentication with bcrypt hashing
- Credential encryption at rest via `CHOWKIDAR_SECRET_KEY`
- All settings configurable via web UI and persisted to `/config/config.yaml`
- Action history stored in SQLite with daily rotating logs
- Unraid Community Applications template
- AGPL-3.0 license

[0.1.1]: https://github.com/darkmatterforge/chowkidar/releases/tag/v0.1.1
[0.1.0]: https://github.com/darkmatterforge/chowkidar/releases/tag/v0.1.0