#!/usr/bin/env bash
set -euo pipefail

IMAGE_TAG="${1:-chowkidar:ci}"
CONTAINER_NAME="chowkidar-ci"
PORT="18080"
BASE_URL="http://localhost:${PORT}"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup

docker run -d --name "${CONTAINER_NAME}" -p "${PORT}:8080" "${IMAGE_TAG}" >/dev/null

for i in $(seq 1 40); do
  if curl -fsS "${BASE_URL}/api/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  if [[ "$i" -eq 40 ]]; then
    echo "health endpoint did not become ready"
    exit 1
  fi
done

curl -fsS "${BASE_URL}/api/settings" >/dev/null
curl -fsS "${BASE_URL}/api/jobs" >/dev/null
curl -fsS "${BASE_URL}/api/history?limit=1" >/dev/null

curl -fsS -X PUT "${BASE_URL}/api/settings" \
  -H 'Content-Type: application/json' \
  -d '{"monitorIntervalSeconds":35,"retryCount":3,"retryDelaySeconds":6,"workerCount":2,"queueSize":32,"httpClientTimeoutSeconds":15,"httpMaxIdleConns":20,"httpMaxIdleConnsPerHost":10,"httpIdleConnTimeoutSeconds":90,"dockerPingTimeoutSeconds":5,"dockerClientRetryCount":1,"dockerClientRetryDelaySeconds":2,"actionTimeoutSeconds":20,"notificationRatePerSec":5,"action":"restart","externalHostname":"ci-smoke","runScriptPath":"","startExited":false,"requireFilterForExited":true,"sqlitePath":"/config/data/app.db","sqliteBusyTimeoutSeconds":5000,"sqlitePragmas":"journal_mode=WAL;synchronous=NORMAL"}' >/dev/null

JOB_JSON=$(curl -fsS -X POST "${BASE_URL}/api/jobs" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-smoke-job","action":"restart","containerNameFilter":"ci-smoke","enabled":true,"startExited":false}')

JOB_ID=$(echo "${JOB_JSON}" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
if [[ -z "${JOB_ID}" ]]; then
  echo "failed to parse created job id"
  exit 1
fi

curl -fsS -X DELETE "${BASE_URL}/api/jobs/${JOB_ID}" >/dev/null
curl -fsS "${BASE_URL}/api/history?limit=5" >/dev/null

echo "api smoke passed"
