#!/usr/bin/env bash
# Run the Playwright E2E suite locally against a clean, isolated container.
# The dev server on :8080 is left completely untouched.
#
# Usage:
#   ./scripts/run-e2e-local.sh            # full suite
#   ./scripts/run-e2e-local.sh --ui       # Playwright UI mode (headed)
#   ./scripts/run-e2e-local.sh 01-setup   # single spec file
#   SKIP_BUILD=1 ./scripts/run-e2e-local.sh  # reuse existing chowkidar:e2e-local

set -euo pipefail

IMAGE="chowkidar:e2e-local"
APP="chowkidar-e2e-local"
TARGET="e2e-target-local"
PORT=18082
BASE_URL="http://localhost:${PORT}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ── Colours ─────────────────────────────────────────────────────────────────
GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
info()  { echo -e "${GREEN}▶ $*${NC}"; }
warn()  { echo -e "${YELLOW}⚠ $*${NC}"; }
error() { echo -e "${RED}✖ $*${NC}"; }

# ── Cleanup on exit ──────────────────────────────────────────────────────────
cleanup() {
  info "Cleaning up containers..."
  docker rm -f "${APP}" "${TARGET}" e2e-unhealthy >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ── 1. Build image (skip with SKIP_BUILD=1) ──────────────────────────────────
if [[ "${SKIP_BUILD:-0}" == "1" ]]; then
  warn "Skipping build (SKIP_BUILD=1) — using existing ${IMAGE}"
else
  info "Building production image ${IMAGE}..."
  docker build -t "${IMAGE}" "${ROOT}" --quiet
  info "Build complete."
fi

# ── 2. Start test containers ──────────────────────────────────────────────────
info "Starting always-healthy target container..."
docker run -d --name "${TARGET}" \
  --health-cmd="echo ok" \
  --health-interval=3s \
  --health-timeout=2s \
  --health-retries=1 \
  alpine:3 sh -c "while true; do sleep 5; done" >/dev/null

info "Starting e2e-unhealthy container (goes unhealthy after 15s)..."
docker run -d --name "e2e-unhealthy" \
  --health-cmd='[ -f /tmp/ok ]' \
  --health-interval=3s \
  --health-timeout=2s \
  --health-retries=1 \
  alpine:3 sh -c 'touch /tmp/ok; sleep 15; rm -f /tmp/ok; while true; do sleep 5; done' >/dev/null

# ── 3. Start the app on an isolated port with a clean tmp config ─────────────
info "Starting app on port ${PORT}..."
docker run -d \
  --name "${APP}" \
  -p "${PORT}:8080" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  "${IMAGE}" >/dev/null

info "Waiting for health endpoint..."
for i in $(seq 1 60); do
  if curl -fsS "${BASE_URL}/api/health" >/dev/null 2>&1; then
    info "App is healthy."
    break
  fi
  sleep 1
  if [[ "${i}" -eq 60 ]]; then
    error "App did not become healthy after 60s."
    docker logs "${APP}"
    exit 1
  fi
done

# ── 4. Bootstrap a monitoring job covering both test containers ───────────────
info "Creating monitoring job for test containers..."
curl -s -X POST "${BASE_URL}/api/jobs" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"e2e-monitor\",\"action\":\"restart\",\"containerNameFilter\":\"${TARGET},e2e-unhealthy\",\"monitorIntervalSeconds\":8,\"enabled\":true,\"startExited\":false}" \
  >/dev/null
# Give the app one poll cycle to discover and list both containers
sleep 10

# ── 5. Run Playwright ─────────────────────────────────────────────────────────
# any tests run — no docker exec needed here.
info "Running Playwright tests against ${BASE_URL}..."
cd "${ROOT}/tests/e2e"

BASE_URL="${BASE_URL}" \
E2E_CONTAINER_NAME="${APP}" \
E2E_USERNAME="testadmin" \
E2E_PASSWORD="TestPassword1!" \
  npx playwright test "$@"
