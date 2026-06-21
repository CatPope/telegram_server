#!/usr/bin/env bash
set -euo pipefail

# send.sh — send a direct notification via POST /v1/messages/direct
#
# Usage:
#   send.sh <recipients_json> <app_id> <text>
#
# Arguments:
#   recipients_json  JSON array of int64 Telegram user IDs, e.g. '[12345678]'
#   app_id           Registered application ID
#   text             Notification text to deliver
#
# Required env vars:
#   TELEGRAM_SERVER_URL  Base URL of the telegram_server
#   TELEGRAM_API_KEY     Bearer token with messages.direct.send capability

[ -z "${TELEGRAM_SERVER_URL:-}" ] && echo "TELEGRAM_SERVER_URL required" >&2 && exit 2
[ -z "${TELEGRAM_API_KEY:-}" ] && echo "TELEGRAM_API_KEY required" >&2 && exit 2

if [ "$#" -lt 3 ]; then
  echo "Usage: send.sh <recipients_json> <app_id> <text>" >&2
  exit 1
fi

RECIPIENTS="$1"
APP_ID="$2"
TEXT="$3"

BODY="$(printf '{"recipients":%s,"app_id":"%s","envelope":{"text":"%s","schema_version":1}}' \
  "$RECIPIENTS" "$APP_ID" "$TEXT")"

curl -sf \
  -X POST \
  -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
  -H "Content-Type: application/json" \
  -d "$BODY" \
  "${TELEGRAM_SERVER_URL}/v1/messages/direct"
