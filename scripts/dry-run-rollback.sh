#!/usr/bin/env bash
# dry-run-rollback.sh — Phase 7 rollback dry-run test.
# Simulates a broken deploy then verifies rollback restores /healthz within 60s.
# Exit 0: PASS. Exit 1: rollback timeout. Exit 2: broken image served 200 (test broken).
set -euo pipefail

HEALTHZ_URL="${HEALTHZ_URL:-http://localhost:8080/healthz}"
APP_IMAGE="telegram_server-app"

start_ts=$(date +%s)

echo "==> [1/7] Tagging current :latest as :previous"
docker tag "${APP_IMAGE}:latest" "${APP_IMAGE}:previous"

echo "==> [2/7] Building deliberately-broken image"
docker build -f Dockerfile.broken -t "${APP_IMAGE}:latest" .

echo "==> [3/7] Restarting app with broken image"
docker compose up -d --no-build app

echo "==> [4/7] Polling /healthz for 30s — expecting FAILURE"
broken=false
for i in $(seq 1 30); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "${HEALTHZ_URL}" 2>/dev/null || true)
  if [ "${code}" = "200" ]; then
    echo "ERROR: /healthz returned 200 with broken image — rollback test is invalid" >&2
    exit 2
  fi
  sleep 1
done
echo "    confirmed: /healthz not 200 during broken window"

echo "==> [5/7] Rolling back: restoring :previous as :latest"
docker tag "${APP_IMAGE}:previous" "${APP_IMAGE}:latest"
docker compose up -d --no-build app

echo "==> [6/7] Polling /healthz for 60s — expecting 200"
recovered=false
for i in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w "%{http_code}" "${HEALTHZ_URL}" 2>/dev/null || true)
  if [ "${code}" = "200" ]; then
    recovered=true
    break
  fi
  sleep 1
done

if [ "${recovered}" != "true" ]; then
  echo "FAIL: /healthz did not return 200 within 60s after rollback" >&2
  exit 1
fi

echo "==> [7/7] Cleanup: removing :previous tag"
docker image rm -f "${APP_IMAGE}:previous" 2>/dev/null || true

end_ts=$(date +%s)
elapsed=$(( end_ts - start_ts ))
echo "PASS: rollback restored /healthz in ${elapsed}s"
