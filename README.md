# Sky Tower

Lightweight Docker health monitor. Watches container health events and reacts with configurable actions (restart, stop, run-script, webhook, auto-heal). Multi-provider notifications via Apprise.

## Features

- Docker health polling (`health=unhealthy`) and exited container auto-start
- Actions: `restart`, `start`, `stop`, `none`, `run-script`, `webhook`, `auto-heal`
- Per-job retry count, retry delay, and monitoring interval controls
- Notification profiles: Discord, Slack, Telegram, ntfy, Gotify, Pushover, SMTP, and raw Apprise URLs
- Per-provider notification templates for 7 lifecycle events
- Web UI: dashboard, jobs, notification profiles, action history, settings
- Config persisted to `/config` (jobs, notifications, history, config.yaml)
- Docker socket diagnostics (socket-proxy mode supported)

## Quick Start

```bash
docker compose up -d
```

Open: `http://localhost:8080`

## Docker CLI

```bash
docker run -d \
  --name chowkidar \
  --restart unless-stopped \
  -p 8080:8080 \
  -v /mnt/user/appdata/chowkidar:/config \
  -v /var/run/docker.sock:/var/run/docker.sock \
  chowkidar/chowkidar:latest
```

## Docker Socket Proxy (recommended for security)

```bash
DOCKER_HOST=tcp://docker-socket-proxy:2375 docker compose --profile socket-proxy up -d
```

## Build & Publish

```bash
# Local build + test
docker build -t chowkidar:ci .
bash scripts/api_smoke.sh chowkidar:ci

# Multi-arch publish (CI handles this automatically on push to main)
docker buildx build --platform linux/amd64,linux/arm64 \
  -t chowkidar/chowkidar:latest --push .
```

## Environment Variables

### Core

| Variable | Default | Description |
|---|---|---|
| `APP_PORT` | `8080` | HTTP port the server listens on |
| `APP_PATH` | `/config` | Base data directory (config files, SQLite, logs) |
| `ACTION` | `restart` | Default action: `restart` / `start` / `stop` / `none` / `run-script` / `webhook` / `auto-heal` |
| `RETRY_COUNT` | `3` | Action retry attempts |
| `RETRY_DELAY` | `10` | Seconds between retries |
| `WORKER_COUNT` | `2` | Parallel action workers |
| `QUEUE_SIZE` | `64` | Action queue capacity |
| `ACTION_TIMEOUT_SECONDS` | `20` | Per-action timeout |

### Container Filtering

| Variable | Default | Description |
|---|---|---|
| `CONTAINER_NAME` | — | CSV of container names to monitor |
| `CONTAINER_LABEL` | — | Substring match against labels |
| `CONTAINER_ENV_VAR` | — | Substring match against container env var values |
| `START_EXITED` | `false` | Auto-start exited containers |
| `REQUIRE_FILTER_FOR_EXITED` | `true` | Require a filter before acting on exited containers |

### Notifications

| Variable | Default | Description |
|---|---|---|
| `APPRISE_NOTIFICATION_SERVICES` | — | Comma-separated Apprise URLs (alternative to UI profiles) |
| `NOTIFICATION_RATE_PER_SEC` | `5` | Notification rate limit |
| `NOTIFICATION_COOLDOWN_SECONDS` | `3600` | Minimum seconds between repeated notifications per container |
| `EXTERNAL_HOSTNAME` | — | Hostname shown in notification payloads |

### Docker

| Variable | Default | Description |
|---|---|---|
| `DOCKER_SOCKET_PATH` | `/var/run/docker.sock` | Docker socket path inside the container |
| `DOCKER_HOST` | — | Override Docker host (e.g. `tcp://socket-proxy:2375`) |
| `DOCKER_PING_TIMEOUT_SECONDS` | `5` | Daemon liveness ping timeout |
| `DOCKER_CLIENT_RETRY_COUNT` | `1` | Docker client retry attempts |
| `DOCKER_CLIENT_RETRY_DELAY_SECONDS` | `2` | Seconds between Docker client retries |

### Scripting / Webhook

| Variable | Default | Description |
|---|---|---|
| `RUN_SCRIPT_PATH` | — | Script path for the `run-script` action |
| `WEBHOOK_URL` | — | Endpoint for the `webhook` action |
| `WEBHOOK_ROOT_KEY` | `event` | Root JSON key in the webhook payload |

### HTTP Client

| Variable | Default | Description |
|---|---|---|
| `HTTP_CLIENT_TIMEOUT_SECONDS` | `15` | Global HTTP timeout |
| `HTTP_MAX_IDLE_CONNS` | `20` | Max idle connections |
| `HTTP_MAX_IDLE_CONNS_PER_HOST` | `10` | Max idle connections per host |
| `HTTP_IDLE_CONN_TIMEOUT_SECONDS` | `90` | Idle connection timeout |

### Persistence

| Variable | Default | Description |
|---|---|---|
| `SQLITE_PATH` | `APP_PATH/data/app.db` | SQLite database path |
| `SQLITE_BUSY_TIMEOUT_SECONDS` | `5000` | SQLite busy timeout (ms) |
| `SQLITE_PRAGMAS` | `journal_mode=WAL;synchronous=NORMAL` | SQLite pragmas |

### Display & Logging

| Variable | Default | Description |
|---|---|---|
| `DISPLAY_TIMEZONE` | UTC | IANA timezone for web UI timestamps (e.g. `America/New_York`) |
| `SERVER_TIMEZONE` | UTC | IANA timezone for internal scheduling and log timestamps |
| `PRIMARY_BASE_URL` | — | Public base URL for links in notifications |
| `LOG_LEVEL` | `info` | Log level: `debug` / `info` / `warn` / `error` |
| `VERBOSE_LOGS` | `false` | Emit verbose debug output |
| `LOG_TO_FILE` | `true` | Write logs to `APP_PATH/logs/` |
| `LOG_RETENTION_DAYS` | `7` | Log file retention in days |

## API Endpoints

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
| `GET` | `/api/history` | Action history (`?limit=50`) |
| `POST` | `/api/test-notification` | Send a test notification |
| `POST` | `/api/action` | Trigger a manual action |

## Unraid

Template: `unraid/chowkidar.xml`. Map `/mnt/user/appdata/chowkidar` → `/config`.
