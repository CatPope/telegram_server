#!/usr/bin/env bash
# smoke-adminui.sh — admin UI live smoke test
#
# compose 스택 위에서 관리자 UI의 핵심 운영 흐름을 end-to-end로 실행한다:
#   1. GET  /login → POST /login          (세션 + CSRF)
#   2. POST /apps                         (앱 등록, /admin API 경유)
#   3. POST /keys                         (키 발급, 평문 1회 응답에서 추출)
#   4. POST /v1/messages/direct           (발급 키로 대상 서버 직접 호출 → 200)
#   5. GET  /audit?stage=key_issued       (감사 뷰어에 key_issued 행)
#   6. POST /keys/{app}/{p}/revoke        (키 폐기)
#   7. POST /v1/messages/direct           (폐기 키 → 401)
#   8. GET  /audit?stage=key_revoked      (감사 뷰어에 key_revoked 행)
#
# Exit codes:
#   0 = 모든 시나리오 PASS
#   1 = 한 개 이상 시나리오 FAIL
#   2 = 필수 도구(curl) 누락
#
# 환경 변수 (모두 선택, 아래 기본값):
#   ADMINUI_URL              기본 http://127.0.0.1:8081
#   TELEGRAM_SERVER_URL      기본 http://127.0.0.1:8080
#   ADMINUI_SMOKE_PASSWORD   기본 change-me-admin-pw (compose placeholder)
#   SMOKE_RECIPIENT_ID       기본 100000042 (migration 0003 seed user)
#   SMOKE_VERBOSE            1 이면 응답 본문 dump

set -euo pipefail

ADMINUI_URL="${ADMINUI_URL:-http://127.0.0.1:8081}"
TELEGRAM_SERVER_URL="${TELEGRAM_SERVER_URL:-http://127.0.0.1:8080}"
ADMINUI_SMOKE_PASSWORD="${ADMINUI_SMOKE_PASSWORD:-change-me-admin-pw}"
SMOKE_RECIPIENT_ID="${SMOKE_RECIPIENT_ID:-100000042}"
SMOKE_VERBOSE="${SMOKE_VERBOSE:-0}"

fail_count=0

require() {
    if ! command -v "$1" >/dev/null 2>&1; then
        printf 'smoke-adminui: required tool "%s" not found in PATH\n' "$1" >&2
        exit 2
    fi
}
require curl

log_pass() { printf '  [PASS] %s\n' "$1"; }
log_fail() { printf '  [FAIL] %s — %s\n' "$1" "$2"; fail_count=$((fail_count + 1)); }

dump() { [ "$SMOKE_VERBOSE" = "1" ] && printf '    body: %s\n' "$(cat "$1" 2>/dev/null || true)" || true; }

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
jar="$workdir/cookies.txt"

# 로그인/발급 페이지 HTML에서 hidden csrf_token 값을 추출한다.
extract_csrf() {
    grep -o 'name="csrf_token" value="[0-9a-f]*"' "$1" | head -1 | sed 's/.*value="\([0-9a-f]*\)"/\1/'
}

# epoch+RANDOM 조합으로 재실행/동일 초 충돌을 피한다. prefix는 ^[a-z0-9]{4,16}$
# 제약이 있어 뒤 8자만 사용. dev 스택에는 실행마다 smoke 앱이 남는다(의도된
# tradeoff — 마지막에 키는 revoke되고 앱은 비활성 대상이 아님).
sfx="$(date +%s)${RANDOM}"
APP_ID="smokeui${sfx}"
KEY_PREFIX="sm${sfx: -8}"

# --- 1. login --------------------------------------------------------------
printf '== Scenario 1: login ==\n'
login_page_status=$(curl -s -o "$workdir/login.html" -w '%{http_code}' -m 5 \
    -c "$jar" "${ADMINUI_URL}/login" || true)
csrf=$(extract_csrf "$workdir/login.html" || true)

if [ "$login_page_status" = "200" ] && [ -n "$csrf" ]; then
    login_status=$(curl -s -o "$workdir/login_post.html" -w '%{http_code}' -m 5 \
        -b "$jar" -c "$jar" \
        -X POST "${ADMINUI_URL}/login" \
        --data-urlencode "password=${ADMINUI_SMOKE_PASSWORD}" \
        --data-urlencode "csrf_token=${csrf}" || true)
    if [ "$login_status" = "303" ]; then
        log_pass "login succeeded (303 → /)"
    else
        log_fail "login" "expected 303, got ${login_status}"
    fi
else
    log_fail "login page" "GET /login status=${login_page_status}, csrf token found=$([ -n "$csrf" ] && echo yes || echo no)"
fi

# 인증 후 페이지에서 세션 결합 CSRF 토큰을 갱신해 두는 헬퍼
fetch_csrf() {
    curl -s -o "$workdir/page.html" -m 5 -b "$jar" "$1" >/dev/null || true
    extract_csrf "$workdir/page.html"
}

# --- 2. register app --------------------------------------------------------
printf '== Scenario 2: register app %s ==\n' "$APP_ID"
csrf=$(fetch_csrf "${ADMINUI_URL}/apps/new" || true)
app_status=$(curl -s -o "$workdir/app_create.html" -w '%{http_code}' -m 10 \
    -b "$jar" \
    -X POST "${ADMINUI_URL}/apps" \
    --data-urlencode "csrf_token=${csrf}" \
    --data-urlencode "id=${APP_ID}" \
    --data-urlencode "name=adminui smoke app" \
    --data-urlencode "min_grade=user" \
    --data-urlencode "capabilities=messages.direct.send" || true)

if [ "$app_status" = "303" ]; then
    log_pass "app registered (303 → /apps/${APP_ID})"
else
    log_fail "app register" "expected 303, got ${app_status}"
    dump "$workdir/app_create.html"
fi

# --- 3. issue key ------------------------------------------------------------
printf '== Scenario 3: issue key (prefix %s) ==\n' "$KEY_PREFIX"
csrf=$(fetch_csrf "${ADMINUI_URL}/keys?new=1" || true)
issue_status=$(curl -s -o "$workdir/key_issued.html" -w '%{http_code}' -m 10 \
    -b "$jar" \
    -X POST "${ADMINUI_URL}/keys" \
    --data-urlencode "csrf_token=${csrf}" \
    --data-urlencode "app_id=${APP_ID}" \
    --data-urlencode "prefix=${KEY_PREFIX}" \
    --data-urlencode "label=smoke-adminui" || true)

plaintext=$(grep -o "tg_${KEY_PREFIX}_[0-9a-f]\{16,\}" "$workdir/key_issued.html" | head -1 || true)
if [ "$issue_status" = "200" ] && [ -n "$plaintext" ]; then
    log_pass "key issued (plaintext extracted, prefix ${KEY_PREFIX})"
else
    log_fail "key issue" "status=${issue_status}, plaintext found=$([ -n "$plaintext" ] && echo yes || echo no)"
    dump "$workdir/key_issued.html"
fi

# --- 4. direct message with issued key ---------------------------------------
printf '== Scenario 4: POST /v1/messages/direct with issued key ==\n'
direct_body=$(printf '{"recipients":[%s],"app_id":"%s","envelope":{"text":"smoke-adminui probe","schema_version":1}}' \
    "$SMOKE_RECIPIENT_ID" "$APP_ID")
direct_status=$(curl -s -o "$workdir/direct.json" -w '%{http_code}' -m 10 \
    -X POST "${TELEGRAM_SERVER_URL}/v1/messages/direct" \
    -H "Authorization: Bearer ${plaintext}" \
    -H "Content-Type: application/json" \
    -d "$direct_body" || true)

if [ "$direct_status" = "200" ]; then
    log_pass "/v1/messages/direct returned 200 with issued key"
else
    log_fail "/v1/messages/direct (issued key)" "expected 200, got ${direct_status}"
fi
dump "$workdir/direct.json"

# --- 5. audit viewer shows key_issued ----------------------------------------
printf '== Scenario 5: audit viewer key_issued row ==\n'
audit_status=$(curl -s -o "$workdir/audit_issued.html" -w '%{http_code}' -m 10 \
    -b "$jar" "${ADMINUI_URL}/audit?stage=key_issued&app_id=${APP_ID}" || true)

# stage 드롭다운/필터 폼에도 key_issued·app_id 문자열이 echo되므로,
# 결과 테이블 셀 단위(배지 스팬 >...</, mono 셀)로 매칭해야 실제 행 존재를 검증한다.
if [ "$audit_status" = "200" ] && grep -q ">key_issued</span>" "$workdir/audit_issued.html" \
        && grep -q "class=\"mono\">${APP_ID}<" "$workdir/audit_issued.html"; then
    log_pass "audit viewer shows key_issued for ${APP_ID}"
else
    log_fail "audit key_issued" "status=${audit_status} or row missing"
    dump "$workdir/audit_issued.html"
fi

# --- 6. revoke key ------------------------------------------------------------
printf '== Scenario 6: revoke key ==\n'
csrf=$(fetch_csrf "${ADMINUI_URL}/keys" || true)
revoke_status=$(curl -s -o "$workdir/revoke.html" -w '%{http_code}' -m 10 \
    -b "$jar" \
    -X POST "${ADMINUI_URL}/keys/${APP_ID}/${KEY_PREFIX}/revoke" \
    --data-urlencode "csrf_token=${csrf}" \
    --data-urlencode "confirm=1" || true)

if [ "$revoke_status" = "303" ]; then
    log_pass "key revoked (303)"
else
    log_fail "key revoke" "expected 303, got ${revoke_status}"
    dump "$workdir/revoke.html"
fi

# --- 7. revoked key must be rejected ------------------------------------------
printf '== Scenario 7: revoked key → 401 ==\n'
revoked_status=$(curl -s -o "$workdir/direct_revoked.json" -w '%{http_code}' -m 10 \
    -X POST "${TELEGRAM_SERVER_URL}/v1/messages/direct" \
    -H "Authorization: Bearer ${plaintext}" \
    -H "Content-Type: application/json" \
    -d "$direct_body" || true)

if [ "$revoked_status" = "401" ]; then
    log_pass "revoked key rejected with 401"
else
    log_fail "revoked key" "expected 401, got ${revoked_status}"
fi
dump "$workdir/direct_revoked.json"

# --- 8. audit viewer shows key_revoked -----------------------------------------
printf '== Scenario 8: audit viewer key_revoked row ==\n'
audit2_status=$(curl -s -o "$workdir/audit_revoked.html" -w '%{http_code}' -m 10 \
    -b "$jar" "${ADMINUI_URL}/audit?stage=key_revoked&app_id=${APP_ID}" || true)

if [ "$audit2_status" = "200" ] && grep -q ">key_revoked</span>" "$workdir/audit_revoked.html" \
        && grep -q "class=\"mono\">${APP_ID}<" "$workdir/audit_revoked.html"; then
    log_pass "audit viewer shows key_revoked for ${APP_ID}"
else
    log_fail "audit key_revoked" "status=${audit2_status} or row missing"
    dump "$workdir/audit_revoked.html"
fi

# --- Summary -------------------------------------------------------------------
printf '\n'
if [ "$fail_count" -eq 0 ]; then
    printf 'smoke-adminui: ALL PASS\n'
    exit 0
else
    printf 'smoke-adminui: %d FAIL\n' "$fail_count" >&2
    exit 1
fi
