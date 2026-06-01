# Chowkidar

> Lightweight Docker health monitor with configurable restart and notification actions.

Chowkidar watches your containers for `unhealthy` health events and exited states, then reacts automatically — restarting, stopping, or running a custom script — and sends notifications via Discord, Slack, Telegram, email, and more.

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Build](https://github.com/darkmatterforge/chowkidar/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/darkmatterforge/chowkidar/actions/workflows/docker-publish.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/darkmatterforge/chowkidar)](https://hub.docker.com/r/darkmatterforge/chowkidar)
[![Image Size](https://img.shields.io/docker/image-size/darkmatterforge/chowkidar/latest)](https://hub.docker.com/r/darkmatterforge/chowkidar)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![GitHub Stars](https://img.shields.io/github/stars/darkmatterforge/chowkidar?style=flat)](https://github.com/darkmatterforge/chowkidar/stargazers)

---

## Features

- **Container monitoring** — polls Docker for `unhealthy` and exited containers
- **Configurable actions** — `restart`, `start`, `stop`, `none`, `run-script` per job or globally
- **Per-job overrides** — individual retry count, retry delay, monitoring interval, and action timeout
- **Multi-provider notifications** — Discord, Slack, Telegram, ntfy, Gotify, Pushover, SMTP/email, raw Apprise URLs
- **Notification templates** — customise message content per provider per lifecycle event
- **Jobs system** — define targeted monitoring rules with container name/label/env var filters
- **Multi-Docker-host** — monitor containers across multiple Docker hosts or sockets
- **Web UI** — dashboard, jobs, notification profiles, action history, settings
- **Authentication** — optional username/password protection with bcrypt hashing
- **Encryption** — notification credentials encrypted at rest via `CHOWKIDAR_SECRET_KEY`
- **3 dashboard layouts** — Card List, Compact Table, Status Grid
- **Light/dark/auto theme**
- **Persistent config** — everything stored under `/config` (YAML + SQLite + logs)
- **Docker socket proxy** support for reduced attack surface

---

## Quick Start

```bash
docker compose up -d
```

Open `http://localhost:8080`

---

## Development

Runs with live reload via [Air](https://github.com/air-verse/air). Go file changes rebuild the binary automatically; `web/` is served directly from disk so HTML/CSS/JS changes are visible on refresh with no rebuild.

```bash
docker compose -f docker-compose.dev.yml up --build
```

Open `http://localhost:8080`

---

## Docker CLI

```bash
docker run -d \
  --name chowkidar \
  --restart unless-stopped \
  -p 8080:8080 \
  -e CHOWKIDAR_SECRET_KEY=your-long-random-secret \
  -v /mnt/user/appdata/chowkidar:/config \
  -v /var/run/docker.sock:/var/run/docker.sock \
  darkmatterforge/chowkidar:latest
```

---

## Environment Variables

Most settings are configured through the **web UI** (Settings page) and persisted to `/config/config.yaml`. The env vars below are the ones you typically set at container creation time. Every env var still overrides the config file if set.

### Required at startup

| Variable | Default | Description |
|---|---|---|
| `CHOWKIDAR_SECRET_KEY` | — | Encryption key for notification credentials. Set to a random 32+ char string. If unset, credentials are stored as plaintext. **Cannot be changed after first use.** |
| `APP_PATH` | `/config` | Base directory for config, database and logs. Must match your volume mount. |
| `APP_PORT` | `8080` | HTTP port the server listens on. |

### Docker connectivity

Chowkidar connects to Docker via the mounted socket by default. Additional Docker hosts (remote or multi-host) are added through **Settings → Docker Hosts** in the web UI.

| Variable | Default | Description |
|---|---|---|
| `DOCKER_HOST` | — | Override the primary Docker endpoint (e.g. `tcp://socket-proxy:2375` if using a socket proxy). Leave unset to use the mounted socket. |
| `DOCKER_SOCKET_PATH` | `/var/run/docker.sock` | Path to the mounted socket inside the container. Only change if using a non-standard socket location. |

### Container filtering

Useful to set at the container level so monitoring starts correctly on first boot:

| Variable | Default | Description |
|---|---|---|
| `CONTAINER_NAME` | — | Comma-separated container names to monitor. Leave empty to watch all. |
| `CONTAINER_LABEL` | — | Watch only containers with this label. |
| `CONTAINER_ENV_VAR` | — | Watch only containers with this env var value. |

### Logging

| Variable | Default | Description |
|---|---|---|
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. Useful for troubleshooting at startup. |

### Advanced overrides

All of these are also configurable via the web UI. Set as env vars only if you need to enforce values that the UI cannot override (e.g. in a managed deployment).

| Variable | Default | Description |
|---|---|---|
| `ACTION` | `restart` | Default action: `restart` / `start` / `stop` / `none` / `run-script` |
| `RETRY_COUNT` | `3` | Action retry attempts |
| `RETRY_DELAY` | `10` | Seconds between retries |
| `WORKER_COUNT` | `2` | Parallel action workers |
| `QUEUE_SIZE` | `64` | Action queue capacity |
| `ACTION_TIMEOUT_SECONDS` | `20` | Per-action timeout (seconds) |
| `STARTUP_DELAY_SECONDS` | `0` | Seconds to wait before monitoring begins |
| `START_EXITED` | `false` | Auto-start exited containers |
| `REQUIRE_FILTER_FOR_EXITED` | `true` | Require a filter before acting on exited containers |
| `APPRISE_NOTIFICATION_SERVICES` | — | Comma-separated Apprise URLs (alternative to UI notification profiles) |
| `NOTIFICATION_RATE_PER_SEC` | `5` | Max notifications per second |
| `NOTIFICATION_COOLDOWN_SECONDS` | `3600` | Min seconds between repeated notifications per container |
| `EXTERNAL_HOSTNAME` | — | Hostname shown in notification payloads |
| `PRIMARY_BASE_URL` | — | Public base URL for links in notifications |
| `RUN_SCRIPT_PATH` | — | Script path for the `run-script` action |
| `DISPLAY_TIMEZONE` | UTC | IANA timezone for web UI timestamps |
| `SERVER_TIMEZONE` | UTC | IANA timezone for scheduling |
| `LOG_TO_FILE` | `true` | Write logs to `$APP_PATH/logs/` |
| `LOG_RETENTION_DAYS` | `7` | Log file retention in days |
| `SQLITE_PATH` | `$APP_PATH/data/app.db` | SQLite database path |
| `HTTP_CLIENT_TIMEOUT_SECONDS` | `15` | HTTP client timeout |
| `DOCKER_PING_TIMEOUT_SECONDS` | `5` | Docker daemon liveness ping timeout |
| `DOCKER_CLIENT_RETRY_COUNT` | `1` | Docker client retry attempts |
| `DOCKER_CLIENT_RETRY_DELAY_SECONDS` | `2` | Seconds between Docker client retries |

---

## Notification Providers

Configure via the web UI under **Settings → Notifications**.

| Provider | Notes |
|---|---|
| Discord | Webhook URL |
| Slack | Incoming webhook |
| Telegram | Bot token + chat ID |
| Email (SMTP) | Full SMTP config with optional DKIM signing |
| ntfy | Self-hosted or ntfy.sh |
| Gotify | Self-hosted Gotify server |
| Pushover | Mobile push alerts |
| Webhook | HTTP POST to any URL |
| Apprise URL | Any raw [Apprise](https://github.com/caronc/apprise) service URL |

### Notification Events

Templates can be customised per provider for each lifecycle event:

| Event | Trigger |
|---|---|
| `bootUp` | Chowkidar started and Docker is reachable |
| `unhealthyDetected` | Container health check failed |
| `retrying` | Recovery action attempt N of M |
| `cooldown` | Monitoring paused after action |
| `containerRecovered` | Container returned to healthy |
| `actionFailed` | Action execution error |
| `retryLimit` | Max retries exhausted |

**Template variables:** `{{.ContainerName}}`, `{{.Action}}`, `{{.Reason}}`, `{{.Cycle}}`, `{{.MaxRetries}}`, `{{.Cooldown}}`, `{{.RuleName}}`

---

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/health` | Liveness check |
| `GET` | `/api/diagnostics` | Docker socket diagnostics |
| `GET` | `/api/containers` | Watched containers |
| `GET/PUT` | `/api/settings` | App settings |
| `GET/POST` | `/api/jobs` | List / create jobs |
| `PUT/DELETE` | `/api/jobs/{id}` | Update / delete a job |
| `GET/PUT` | `/api/notifications` | Notification profiles |
| `GET/PUT` | `/api/scripts` | Script allowlist |
| `GET/PUT` | `/api/docker-hosts` | Multi-host profiles |
| `GET` | `/api/docker-hosts/status` | Multi-host status |
| `GET` | `/api/history` | Action history |
| `POST` | `/api/test-notification` | Send a test notification |
| `POST` | `/api/action` | Trigger a manual action |
| `POST` | `/api/auth/login` | Login |
| `POST` | `/api/auth/change-password` | Change password |

---

## Unraid

Install via Community Applications or add the template manually:

```
https://raw.githubusercontent.com/darkmatterforge/chowkidar/main/unraid/chowkidar.xml
```

Map `/mnt/user/appdata/chowkidar` → `/config` and set `CHOWKIDAR_SECRET_KEY`.

---

## License

[AGPL-3.0](LICENSE) — Copyright (C) 2026 Dark Matter Forge.

You may use and build on this software, but any modified version must remain open source under the same terms. See [NOTICE](NOTICE) for full usage terms and trademark information.
