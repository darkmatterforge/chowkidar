# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [0.5.2] - 2026-06-21

### Security

- Automated rebuild: 6 package(s) updated
  - **Updated packages:**
  - libffi-3.5.2-r7 → libffi-3.6.0-r0
  - ncurses-terminfo-base-6.6.20260613-r3 → ncurses-6.6.20260620-r0

## [0.5.1] - 2026-06-20

### Security
- Automated rebuild: wolfi-base updated, go builder updated; 20 package(s) updated; fixed 6 CVE(s)

## [0.5.0] - 2026-06-13

### Added

- **Maintenance windows** — schedule service pauses from the Settings → Maintenance tab. Six strategies supported:
  - `Manual` — a direct on/off toggle; the window is active exactly while the `Active` checkbox is checked
  - `Single Window` — a one-time pause between a fixed start and end datetime (auto-pruned 12 h after it ends)
  - `Cron Expression` — standard 5-field cron (`minute hour day month weekday`) evaluated in the chosen timezone; pauses for a configurable Duration on each tick
  - `Recurring — Interval` — repeats every N days from the window's creation date at a chosen time of day and Duration
  - `Recurring — Day of Week` — repeats on selected weekdays at a chosen time of day and Duration
  - `Recurring — Day of Month` — repeats on selected calendar days at a chosen time of day and Duration (days 29–31 skip months that don't have them)
- **Maintenance targets** — each window targets either specific jobs (by ID) or Docker hosts (by host ID), never both; targeting `local` additionally skips the entire scan cycle for the duration
- **Optional effective date range** — `Effective From` / `Effective To` fields limit when any strategy is active, even Manual; windows outside the range are automatically inactive (no active-flag management needed)
- **Per-window `OnStart` behaviour** — controls what happens to queued or in-flight actions the moment a window opens:
  - `allow-finish` (default) — no interruption; anything already dispatched runs to completion
  - `cancel-queued` — aborts actions whose Docker call has not yet started (logged as `skipped — maintenance window started`); in-flight calls complete normally
  - `force-cancel` — also interrupts Docker calls already in flight via context cancellation; may leave a container mid-restart/-stop — use only when necessary
- **Bell lifecycle notifications** — three alert types complete the pause lifecycle alongside the existing upcoming-reminder (`maintenance_upcoming`): `maintenance_started` fires when a window opens; `maintenance_ended` fires when it closes; both are auto-dismissed after 24 h
- **Auto-prune** — expired Single Window and time-bounded Manual windows are automatically removed from the list 12 h after their end time (no manual cleanup needed)
- **Last / Next occurrence columns** — the windows list shows when each window last ran and when it will next run, computed server-side from schedule math

## [0.4.0] - 2026-06-07

### Added

- **Dashboard group collapse/expand** — service group headers are now clickable; a ▼ chevron toggles the group open or collapsed. Collapsed state is persisted to `localStorage` across page reloads. Group headers show a count badge `(N)`.
- **Dashboard Docker host filter** — a new "All Hosts" dropdown in the primary filter bar lets you narrow the service list to containers relevant to a specific Docker host. Services matched by jobs pinned to a host are shown for that host; all-contexts jobs appear under every host.
- **`dockerHostIDs` in matched-jobs API response** — the `/api/containers` response now includes `dockerHostIDs` on each matched-job summary entry, enabling client-side host filtering without extra round-trips.

- **Multi-context job rules** — each job can now be pinned to one or more Docker contexts via a chip-select UI. Jobs with no context selected run on every host (existing behaviour). Context filter dropdown added to the job list.
- **Per-job health-check script** — jobs now have a `Health Check` section with a `Docker Status` / `Bash Script` toggle. When set to script mode, a custom bash script runs against each matching container; exit 0 = healthy, non-zero = unhealthy (triggers the job action). Dry-run and template picker included, identical to the action script experience.
- **Per-Docker-host monitoring settings** — each extra Docker host gains a collapsible `Monitoring Settings` panel with three per-host fields:
  - `Monitor Interval (s)` — how often to ping this host (default: every global scan cycle)
  - `Ping Timeout (s)` — overrides the global ping timeout for this host
  - `Confirm Offline (s)` — seconds the host must be continuously unreachable before the offline notification fires (default 1800 s / 30 min)
- **Per-Docker-host offline notifications** — each host can have its own notification profiles (chip-select) and two message templates with a dropdown picker:
  - `Offline Message` — sent once when the host goes offline after the confirm window
  - `Back Online Message` — sent once when the host recovers
  - Template variable: `{{.HostName}}`; sent directly to all configured agents (Slack, Discord, Telegram, etc.) without profile template indirection
- **Docker host enable/disable** — hosts can be individually disabled. Disabled hosts are excluded from scanning, ping checks, and offline notifications immediately on save (no restart needed). `Enabled` column added to the Docker Hosts table with green `true` / red `false` pills; built-in Local Docker shows `active`.
- **`RunningContainers` Docker query** — new `dockerhealth.RunningContainers` method used by health-check script scans to query all running containers efficiently.
- **Connectivity state tracking** — `scanOnce` pings each extra host before scanning, tracks `connected` / `offlineSince` / `notified` state per host, and sends offline/recovery notifications on state transitions only (no spam on repeated failures).

### Changed

- **Dashboard layout is now full-width** — removed the 1300 px `max-width` cap so the UI fills the available viewport on wide monitors.
- **Stats bar redesign** — tiles are now flex-based (auto-sizing), centred, with a larger 32 px count value. Refresh button moved to the right of its own toolbar row. Docker Hosts tile shows only the `N/N` count — per-host rows removed from the tile.
- **Heartbeat bars made compact** — bar height reduced from 34 px to 20 px; gap tightened to 2 px.
- **Settings page no longer has a fixed minimum height** — the layout now shrinks to content so sparse tabs (Security) don't show empty space at the bottom.
- **Service cards more compact** — padding reduced (`7px 9px`) and margin between cards narrowed (`4px`) to show more containers without scrolling.

- **Docker host form inputs replaced with checkboxes** — `Enabled` dropdown in Docker host form, Job form (`Enabled` and `Start Exited`), are now checkboxes matching the Notifications tab style.
- **"Apply" button removed** — the Docker Hosts "Apply" button and `activeHostID` concept are removed. Use per-job Docker Context selection instead.
- **`Docker Client Retry Count` removed from Monitoring UI** — no longer user-configurable; backend retains a hardcoded default of 3. Advanced users can still set `dockerClientRetryCount` in `config.yaml`.
- **`Docker Client Retry Delay` removed from Monitoring UI** — same treatment as retry count.
- **`Docker Ping Timeout` moved from Monitoring settings to per-host form** — global value removed from settings UI; each Docker host configures its own ping timeout (default 5 s). Built-in Local Docker uses the config file value or 5 s fallback.
- **Dashboard Docker Hosts card** — shows individual host rows (with dot, name, `Connected`/`Offline`/`Disabled` label) for ≤ 5 hosts; shows `N/N healthy` or `N/N healthy — X offline · Y disabled` summary for 6+ hosts. Disabled hosts are excluded from the N/N count.
- **`DockerHostID string` migrated to `DockerHostIDs []string`** — YAML schema version bumped to v2; existing jobs with a single `dockerHostID` are migrated to a one-element slice on first load.
- **Queue Settings spacing** — form fields now use `gap: 14px 24px` so the grid rows breathe.
- **Message template dropdowns** — Offline Message and Back Online Message now have a collapsible `Message Templates` section in the Docker host form, with a `Templates:` dropdown picker and full-width text inputs.

### Fixed

- **`recordHealthPulses` wrong-host scoping** — health pulses were written using the full unfiltered job list, causing containers matched by jobs pinned to host X to receive a "healthy" pulse on host Y. Fixed by passing `jobsForHost(allJobs, hostID)` for each host independently.

- **`offlineConfirmSeconds` not persisting** — blank form field was sending `0`, which `omitempty` dropped from YAML; JS now defaults to `1800` so the value is always written.
- **Double cooldown / double scan** — jobs with no `dockerHostIDs` were scanned once per configured host, producing duplicate history entries. Resolved by pinning jobs to specific contexts; documented and covered by dual-context runtime tests.
- **Health-check script notifications bypassed profile templates** — `sendDockerHostNotif` now calls `notify.New(p.Service).Send(title, body)` directly so the user's custom message is always delivered verbatim to every agent.

### Tests

- E2E checkbox fixes across `07-docker-hosts.spec.ts` and `08-jobs.spec.ts` (all `selectOption`/`toHaveValue` for enabled fields replaced with `check`/`uncheck`/`toBeChecked`)
- New docker host tests: `Monitoring Settings` collapsible, offline notifications API round-trip, `offlineConfirmSeconds` default persistence, `active` pill for built-in host, message template picker
- New job tests: `Enabled`/`Start Exited` checkbox defaults and API persistence
- New runtime tests in `12-job-runtime.spec.ts`:
  - Disabled job never fires even when container fails
  - History entry records correct action type (`run-script`)
  - Dual-context same-daemon suite: second-host fires, disabled-host skips, all-contexts double-fires, local-only fires fewer times than all-contexts
- Restart persistence tests for all new Docker host fields (`monitorIntervalSeconds`, `pingTimeoutSeconds`, `offlineConfirmSeconds`, `downTemplate`, `recoveryTemplate`, `notifications`) and job `dockerHostIDs` / `healthCheckScript`

### Infrastructure
- Migrated Docker build and runtime images from Alpine to Chainguard (`wolfi-base` + `cgr.dev/chainguard/go`) for reduced CVE surface
- `apk upgrade` runs on every Docker build so all installed packages stay current without waiting for a scheduled patch
- Docker Hub repository description now synced automatically from `README.md` on every release via `peter-evans/dockerhub-description`
- Added `security-patch` workflow: weekly check (Mondays) that detects base image updates, package upgrades, and fixable CVEs — opens an auto-merge PR when a rebuild improves the published image
- Added `security-tag` workflow: pushes the version tag when a security patch PR merges, triggering the full `docker-publish` + `release` chain
- Updated `release.sh`: changelog commit now goes via a PR branch; CI must pass before the PR is admin-merged and the tag is pushed — prevents publishing a broken image
- Fixed dev image (`Dockerfile.dev`): migrated to `wolfi-base`; `air` installed via `GOBIN` so it lands in `$PATH`
- Fixed CI double-run: removed `pull_request` trigger so each commit runs CI exactly once via `push`

### Changed
- Removed global `RetryCount`/`RetryDelay` config fields — retry behaviour is now per-job only; start-exited and manual actions use a single attempt
- Removed `ScriptTimeoutSeconds` per-job field — `actionTimeoutSeconds` now covers both docker actions and bash script execution
- Removed `DefaultAction` from the Settings UI — configurable via `ACTION` env var only; every job requires an explicit action
- Removed dead SQLite config fields (`sqlitePath`, `sqliteBusyTimeoutSeconds`, `sqlitePragmas`) — history is stored in JSON.
- Settings number inputs now enforce `min`/`max` bounds, `inputmode=numeric` for mobile keyboards, and fixed width for visual consistency
- Docker Client Retry Count: no upper cap — set as high as needed for slow-starting Docker daemons
- Monitoring settings tab uses a two-column horizontal label → value layout with dividers
- Settings form grid: max two columns, collapses to one column below 600px
- External Hostname and Primary Base URL validated on blur only (they are optional); saves from any tab are never blocked by these fields
- Required job form fields (`Name`, `Action`, `Container Filter`, `Monitor Interval`, `Action Timeout`) now marked with a red `*`
- Validation errors always appear under their own field — grid layout fix prevents error divs from drifting into the wrong column

### Fixed
- Settings saves were silently blocked when `externalHostname` stored a URL-style value (e.g. from auto-detect); relaxed to accept any non-space string
- `primaryBaseURL` changed from `type="url"` to `type="text"` — prevents browser from showing native red border on optional empty field

### Tests
- E2E bell notification tests now mock `/api/system-alerts` when the server has no active alerts (prior test dismisses all server-side), eliminating spurious skips
- Fixed flaky bash script job and notification tests: wait for tab content / provider fields to be visible before filling
- Added settings persistence tests: verify `primaryBaseURL`, `externalHostname`, `displayTimezone`, `serverTimezone` survive page reload and container restart
- Added monitoring defaults test: all critical numeric settings have sensible non-zero values on fresh install
- Removed stale E2E tests that referenced removed fields (`#action` hidden select, `retryCount`, `retryDelaySeconds`)
- `scripts/run-e2e-local.sh` now passes `E2E_CONTAINER_NAME` so restart persistence tests work locally

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
- Unraid Community Applications template
- AGPL-3.0 license

[Unreleased]: https://github.com/darkmatterforge/chowkidar/compare/v0.5.2...HEAD
[0.5.2]: https://github.com/darkmatterforge/chowkidar/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/darkmatterforge/chowkidar/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/darkmatterforge/chowkidar/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/darkmatterforge/chowkidar/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/darkmatterforge/chowkidar/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/darkmatterforge/chowkidar/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/darkmatterforge/chowkidar/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/darkmatterforge/chowkidar/releases/tag/v0.1.0
