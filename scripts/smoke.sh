#!/usr/bin/env bash
# smoke.sh — telegram_server live smoke test
#
# 로컬 또는 staging docker stack 위에서 핵심 3 시나리오를 cURL로 호출한다:
#   1. GET /healthz                  (auth 없음, 가장 빠른 liveness)
#   2. POST /v1/messages/direct      (bearer auth + Telegram dispatch end-to-end)
#   3. GET /admin/audit/search       (bearer auth + audit chain 가시성)
#
# 추가로 mocktelegram /test/calls 를 검사해 send-notification 이 실제로
# sendMessage 까지 전달됐는지 확인한다 (MOCKTELEGRAM_URL 가 설정됐을 때).
#
# Exit codes:
#   0 = 모든 시나리오 PASS
#   1 = 한 개 이상 시나리오 FAIL
#   2 = 필수 도구(curl/jq) 누락
#
# 환경 변수 (모두 선택, 아래 기본값):
#   TELEGRAM_SERVER_URL    기본 http://localhost:8080
#   TELEGRAM_API_KEY       기본 dev-admin 키 (cmd/devseed seed 값)
#   MOCKTELEGRAM_URL       기본 http://localhost:8090. "" 로 두면 mocktelegram 검증 skip
#   SMOKE_RECIPIENT_ID     기본 100000044 (migration 0003 seed user)
#   SMOKE_APP_ID           기본 deploy-alerts (migration 0004 seed app)
#   SMOKE_VERBOSE          1 이면 모든 응답을 stdout 으로 dump

set -euo pipefail

TELEGRAM_SERVER_URL="${TELEGRAM_SERVER_URL:-http://localhost:8080}"
TELEGRAM_API_KEY="${TELEGRAM_API_KEY:-tg_devadmin_0123456789abcdef0123456789abcdef}"
MOCKTELEGRAM_URL="${MOCKTELEGRAM_URL-http://localhost:8090}"
SMOKE_RECIPIENT_ID="${SMOKE_RECIPIENT_ID:-100000044}"
SMOKE_APP_ID="${SMOKE_APP_ID:-deploy-alerts}"
SMOKE_VERBOSE="${SMOKE_VERBOSE:-0}"

fail_count=0

require() {
    if ! command -v "$1" >/dev/null 2>&1; then
        printf 'smoke: required tool "%s" not found in PATH\n' "$1" >&2
        exit 2
    fi
}
require curl
require jq

log_pass() { printf '  [PASS] %s\n' "$1"; }
log_fail() { printf '  [FAIL] %s — %s\n' "$1" "$2"; fail_count=$((fail_count + 1)); }
log_skip() { printf '  [SKIP] %s — %s\n' "$1" "$2"; }

print_resp() { [ "$SMOKE_VERBOSE" = "1" ] && printf '    body: %s\n' "$1" || true; }

# --- 1. /healthz ----------------------------------------------------------------
printf '== Scenario 1: GET /healthz ==\n'
healthz_status=$(curl -sf -o /tmp/smoke_healthz.json -w '%{http_code}' \
    -m 5 "${TELEGRAM_SERVER_URL}/healthz" || true)

if [ "$healthz_status" = "200" ]; then
    log_pass "/healthz returned 200"
else
    log_fail "/healthz" "expected 200, got ${healthz_status}"
fi
print_resp "$(cat /tmp/smoke_healthz.json 2>/dev/null || true)"

# --- 2. /v1/messages/direct ----------------------------------------------------
printf '== Scenario 2: POST /v1/messages/direct ==\n'
direct_body=$(jq -n \
    --argjson rid "$SMOKE_RECIPIENT_ID" \
    --arg app "$SMOKE_APP_ID" \
    '{recipients: [$rid], app_id: $app, envelope: {text: "smoke.sh probe", schema_version: 1}}')

direct_status=$(curl -sf -o /tmp/smoke_direct.json -w '%{http_code}' \
    -m 10 \
    -X POST "${TELEGRAM_SERVER_URL}/v1/messages/direct" \
    -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
    -H "Content-Type: application/json" \
    -d "$direct_body" || true)

# 0으로 초기화 — scenario 4가 이 값을 참조
direct_delivered=0

if [ "$direct_status" = "200" ]; then
    # API 계약 검증: delivered/skipped/failed counter + recipients[] 가 모두 있어야 함
    if jq -e '(.delivered | type == "number") and (.skipped | type == "number") and (.failed | type == "number") and (.recipients | type == "array")' \
            /tmp/smoke_direct.json >/dev/null 2>&1; then
        direct_delivered=$(jq -r '.delivered' /tmp/smoke_direct.json)
        direct_skipped=$(jq -r '.skipped' /tmp/smoke_direct.json)
        direct_failed=$(jq -r '.failed' /tmp/smoke_direct.json)
        log_pass "/v1/messages/direct returned 200 (delivered=${direct_delivered}, skipped=${direct_skipped}, failed=${direct_failed})"
    else
        log_fail "/v1/messages/direct" "200 OK but response shape missing delivered/skipped/failed/recipients"
    fi
else
    log_fail "/v1/messages/direct" "expected 200, got ${direct_status}"
fi
print_resp "$(cat /tmp/smoke_direct.json 2>/dev/null || true)"

# --- 3. /admin/audit/search ----------------------------------------------------
printf '== Scenario 3: GET /admin/audit/search ==\n'
audit_status=$(curl -sf -o /tmp/smoke_audit.json -w '%{http_code}' \
    -m 10 \
    "${TELEGRAM_SERVER_URL}/admin/audit/search?limit=5" \
    -H "Authorization: Bearer ${TELEGRAM_API_KEY}" || true)

if [ "$audit_status" = "200" ]; then
    if jq -e '.results | type == "array"' /tmp/smoke_audit.json >/dev/null 2>&1; then
        log_pass "/admin/audit/search returned 200 with results[]"
    else
        log_fail "/admin/audit/search" "200 OK but results[] missing"
    fi
else
    log_fail "/admin/audit/search" "expected 200, got ${audit_status}"
fi
print_resp "$(cat /tmp/smoke_audit.json 2>/dev/null || true)"

# --- 4. mocktelegram side-effect (옵션) ---------------------------------------
printf '== Scenario 4: mocktelegram /test/calls (sendMessage 캡처 확인) ==\n'
if [ -z "$MOCKTELEGRAM_URL" ]; then
    log_skip "mocktelegram" "MOCKTELEGRAM_URL 이 비어 있음"
else
    mock_status=$(curl -sf -o /tmp/smoke_mock.json -w '%{http_code}' \
        -m 5 "${MOCKTELEGRAM_URL}/test/calls" || true)

    if [ "$mock_status" = "200" ]; then
        send_count=$(jq -r '[.[] | select(.method == "sendMessage")] | length' \
            /tmp/smoke_mock.json 2>/dev/null || echo 0)
        if [ "$direct_delivered" -ge 1 ]; then
            # 시나리오 2가 실제 전송된 경우에만 mocktelegram 도달 강제
            if [ "${send_count:-0}" -ge "$direct_delivered" ]; then
                log_pass "mocktelegram captured sendMessage (count=${send_count} >= delivered=${direct_delivered})"
            else
                log_fail "mocktelegram" "delivered=${direct_delivered} 인데 sendMessage count=${send_count}"
            fi
        else
            # delivered=0 (recipient seed 없음 등)이면 mocktelegram 도달은 기대하지 않음. 사이드카 reachability 만 확인.
            log_pass "mocktelegram reachable (sendMessage count=${send_count}; 시나리오 2 delivered=0 이라 강제 검증 안 함)"
        fi
    else
        log_skip "mocktelegram" "GET /test/calls returned ${mock_status} (사이드카 미기동?)"
    fi
fi
print_resp "$(cat /tmp/smoke_mock.json 2>/dev/null || true)"

# --- Summary -------------------------------------------------------------------
printf '\n'
if [ "$fail_count" -eq 0 ]; then
    printf 'smoke: ALL PASS\n'
    exit 0
else
    printf 'smoke: %d FAIL\n' "$fail_count" >&2
    exit 1
fi
