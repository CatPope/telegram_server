#!/usr/bin/env bash
set -euo pipefail

# manage.sh — manage user grades and subscriptions
#
# Usage:
#   manage.sh promote <telegram_id> <grade>
#   manage.sh subscribe <telegram_id> <app_id>
#   manage.sh unsubscribe <telegram_id> <app_id>
#
# Actions:
#   promote      PATCH /admin/users/{telegram_id}  — change user grade
#   subscribe    POST  /admin/users/{telegram_id}/subscriptions/{app_id}
#   unsubscribe  DELETE /admin/users/{telegram_id}/subscriptions/{app_id}
#
# Required env vars:
#   TELEGRAM_SERVER_URL  Base URL of the telegram_server
#   TELEGRAM_API_KEY     Bearer token with operator-tier capabilities

[ -z "${TELEGRAM_SERVER_URL:-}" ] && echo "TELEGRAM_SERVER_URL required" >&2 && exit 2
[ -z "${TELEGRAM_API_KEY:-}" ] && echo "TELEGRAM_API_KEY required" >&2 && exit 2

if [ "$#" -lt 2 ]; then
  echo "Usage: manage.sh <action> <telegram_id> [grade|app_id]" >&2
  exit 1
fi

ACTION="$1"
TELEGRAM_ID="$2"

case "$ACTION" in
  promote)
    if [ "$#" -lt 3 ]; then
      echo "Usage: manage.sh promote <telegram_id> <grade>" >&2
      exit 1
    fi
    GRADE="$3"
    BODY="$(printf '{"grade":"%s"}' "$GRADE")"
    curl -sf \
      -X PATCH \
      -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
      -H "Content-Type: application/json" \
      -d "$BODY" \
      "${TELEGRAM_SERVER_URL}/admin/users/${TELEGRAM_ID}"
    ;;

  subscribe)
    if [ "$#" -lt 3 ]; then
      echo "Usage: manage.sh subscribe <telegram_id> <app_id>" >&2
      exit 1
    fi
    APP_ID="$3"
    curl -sf \
      -X POST \
      -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
      -H "Content-Type: application/json" \
      -d '{}' \
      "${TELEGRAM_SERVER_URL}/admin/users/${TELEGRAM_ID}/subscriptions/${APP_ID}"
    ;;

  unsubscribe)
    if [ "$#" -lt 3 ]; then
      echo "Usage: manage.sh unsubscribe <telegram_id> <app_id>" >&2
      exit 1
    fi
    APP_ID="$3"
    curl -sf \
      -X DELETE \
      -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
      "${TELEGRAM_SERVER_URL}/admin/users/${TELEGRAM_ID}/subscriptions/${APP_ID}"
    ;;

  *)
    echo "Unknown action: $ACTION. Must be one of: promote, subscribe, unsubscribe" >&2
    exit 1
    ;;
esac
