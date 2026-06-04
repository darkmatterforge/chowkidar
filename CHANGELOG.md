# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Infrastructure
- Migrated Docker build and runtime images from Alpine to Chainguard (Wolfi-base + Chainguard Go) for reduced CVE surface
- Docker Hub repository description now synced automatically from `README.md` on every release via `peter-evans/dockerhub-description`
- Added `security-patch` workflow: daily check that detects base image updates, package upgrades, and fixable CVEs — opens a PR with auto-merge when a rebuild improves the image
- Added `security-tag` workflow: pushes the version tag when a security patch PR merges, triggering the full `docker-publish` + `release` chain
- Updated `release.sh`: changelog commit now goes via a PR branch; CI must pass before the PR is admin-merged and the tag is pushed — prevents publishing a broken image

## [0.3.0] - 2026-06-04

### Added
- **Nav-bar logout button** — door-exit icon shown in the top nav when authentication is enabled, positioned after the notification bell; dismisses session and returns to the login page; disappears immediately when auth is disabled
- **Settings sidebar version display** — bottom of the settings tabs shows the running version (e.g. `v0.3.0`) and an amber "vX.Y.Z available" hint when a newer release exists
- **Build-time version injection** — `APP_VERSION` build-arg baked into the binary via `-ldflags`; Docker images now carry the release tag version; falls back to `dev` for local builds. The `docker-publish` workflow passes the tag automatically so `git tag == /api/health version == CHANGELOG version`
- **Notification bell improvements** — system alerts (failed recovery, paused monitoring, update available) now persist across page refreshes via localStorage; individual ✕ dismiss buttons on each alert; "Mark all read" only visible when there is something to clear
- **App version + update check** — `/api/health` now returns `version` and `bootTime` fields; background goroutine checks GitHub releases every 12 h and surfaces a bell alert when a newer version is available
- **Delete history** — `DELETE /api/history` endpoint and matching **Clear All History** button in Settings → General with a confirmation dialog
- **Status grid improvements** — larger tiles (130 px min), hover lift, stronger border colours, job/action badge, consistent dark-mode gradients
- **`formatTimestamp()` helper** — all UI timestamps now use the Display Timezone from Settings, producing clean `YYYY-MM-DD HH:MM:SS` output instead of browser-locale strings

### Fixed
- **Theme toggle / bell navigating to dashboard** — `bindEvents` was attaching `switchPage` to every `.nav-btn`, including buttons without a `data-page`; scoped to `.nav-btn[data-page]` and added null guard in `switchPage`
- **`monitoring_started` alert timestamp** — was using `lastScan` (updated every scan cycle) which made it appear newer than recovery failures; now uses `bootTime` so it is always chronologically first
- **`DashboardRefreshSeconds` zeroed on Docker Hosts save** — `handleDockerHostsPUT` previously called `ToFileConfig(cfg)` which omitted `DashboardRefreshSeconds` (not in `Config` struct); now loads and patches the existing `FileConfig`
- **Notification bell race condition** — `loadSystemAlerts()` now calls `updateNotifBell()` immediately on completion so alerts appear on page load without waiting for the 5-second background poll
- **Job save feedback hidden inside collapsed form** — `#jobSaveStatus` moved outside `#jobFormPanel` so it persists after the form closes (matches the Notifications tab pattern)
- **Login logo background mismatch** — removed solid `<rect fill="...">` from both stacked SVG logos; they are now transparent and blend with any panel colour
- **Apprise error message cleanup** — `cleanNotifError()` strips the Python log prefix (`2026-06-02 12:43:43,738 - WARNING - ...`) before storing `LastRateLimitError`; the bell now shows the meaningful message only
- **Job filter bar dark mode** — replaced hardcoded `background:#fff` with `background:var(--input-bg)` on search/filter inputs
- **Dev log double-timestamp** — set `[log] time = false` in `.air.toml`; dev logs now match production format
- **Service card names hard to read in dark mode** — added `color:var(--text); font-weight:800` and switched card background to `var(--panel)` with theme-aware border and hover colours
- **`/api/health` unauthenticated** — Docker `HEALTHCHECK` could fail when auth was enabled; health endpoint no longer requires a session cookie
- **Bars history limit silent fallback** — `limit=1000` was silently reduced to 25 because `1000 > 500` failed the cap check; bars requests now allow up to 2000

### Changed
- `action-history.jsonl` renamed to `action-history.json`
- `logs/` and `data/` directories created with `0o755` (was `0o750`) so host users on NAS/Unraid can read log files
- Log files created with `0o644` (was `0o600`) for the same reason
- Bell system alerts sorted: critical events first, `monitoring_started` always last
- Independent 5-second alert poll replaces dashboard-interval piggyback; bell opens with an immediate refresh

### Tests
- 17 new Go backend tests covering health endpoint, `cleanNotifError`, system alerts, history clear/bars limit cap, settings persistence, notification suspension, timezone helpers
- 4 new Playwright e2e suites (12–15): bell persistence/timing/ordering, save feedback banners, login logo transparency, nav logout button (10+ scenarios)

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

[0.3.0]: https://github.com/darkmatterforge/chowkidar/releases/tag/v0.3.0
[0.2.0]: https://github.com/darkmatterforge/chowkidar/releases/tag/v0.2.0
[0.1.1]: https://github.com/darkmatterforge/chowkidar/releases/tag/v0.1.1
[0.1.0]: https://github.com/darkmatterforge/chowkidar/releases/tag/v0.1.0