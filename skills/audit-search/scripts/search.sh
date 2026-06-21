#!/usr/bin/env bash
set -euo pipefail

# search.sh — search the audit log via GET /admin/audit/search
#
# Usage:
#   search.sh [--trace-id <id>] [--app-id <id>] [--stage <stage>]
#             [--since <rfc3339>] [--until <rfc3339>] [--limit <n>]
#
# All flags are optional; omitting all returns the 50 most recent events.
#
# Required env vars:
#   TELEGRAM_SERVER_URL  Base URL of the telegram_server
#   TELEGRAM_API_KEY     Bearer token with audit.search capability

[ -z "${TELEGRAM_SERVER_URL:-}" ] && echo "TELEGRAM_SERVER_URL required" >&2 && exit 2
[ -z "${TELEGRAM_API_KEY:-}" ] && echo "TELEGRAM_API_KEY required" >&2 && exit 2

QUERY=""

add_param() {
  local key="$1"
  local val="$2"
  if [ -n "$QUERY" ]; then
    QUERY="${QUERY}&${key}=${val}"
  else
    QUERY="${key}=${val}"
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --trace-id)
      shift
      add_param "trace_id" "$1"
      ;;
    --app-id)
      shift
      add_param "app_id" "$1"
      ;;
    --stage)
      shift
      add_param "stage" "$1"
      ;;
    --since)
      shift
      # URL-encode the + in RFC3339 timestamps (spaces in some parsers)
      ENCODED="${1/+/%2B}"
      add_param "since" "$ENCODED"
      ;;
    --until)
      shift
      ENCODED="${1/+/%2B}"
      add_param "until" "$ENCODED"
      ;;
    --limit)
      shift
      add_param "limit" "$1"
      ;;
    *)
      echo "Unknown flag: $1" >&2
      echo "Usage: search.sh [--trace-id <id>] [--app-id <id>] [--stage <stage>] [--since <rfc3339>] [--until <rfc3339>] [--limit <n>]" >&2
      exit 1
      ;;
  esac
  shift
done

URL="${TELEGRAM_SERVER_URL}/admin/audit/search"
if [ -n "$QUERY" ]; then
  URL="${URL}?${QUERY}"
fi

curl -sf \
  -X GET \
  -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
  "$URL"
