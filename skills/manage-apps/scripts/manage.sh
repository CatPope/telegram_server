#!/usr/bin/env bash
set -euo pipefail

# manage.sh — manage app lifecycle (create / patch / delete)
#
# Usage:
#   manage.sh create <id> <name> [description] [min_grade] [capabilities_json]
#   manage.sh patch  <id> <json_body>
#   manage.sh delete <id>
#
# Actions:
#   create  POST   /admin/apps          — register a new app
#   patch   PATCH  /admin/apps/{id}     — update app metadata or capabilities
#   delete  DELETE /admin/apps/{id}     — soft-delete the app
#
# Required env vars:
#   TELEGRAM_SERVER_URL  Base URL of the telegram_server
#   TELEGRAM_API_KEY     Bearer token with apps.register capability
#
# NOTE: rate_limit_policies write and /rotate key rotation are not yet
#       implemented server-side (Phase 6/7 follow-ups). See SKILL.md.

[ -z "${TELEGRAM_SERVER_URL:-}" ] && echo "TELEGRAM_SERVER_URL required" >&2 && exit 2
[ -z "${TELEGRAM_API_KEY:-}" ] && echo "TELEGRAM_API_KEY required" >&2 && exit 2

if [ "$#" -lt 2 ]; then
  echo "Usage: manage.sh <action> <id> [...]" >&2
  exit 1
fi

ACTION="$1"
ID="$2"

case "$ACTION" in
  create)
    NAME="${3:-}"
    DESCRIPTION="${4:-}"
    MIN_GRADE="${5:-}"
    CAPABILITIES="${6:-[]}"
    if [ -z "$NAME" ]; then
      echo "Usage: manage.sh create <id> <name> [description] [min_grade] [capabilities_json]" >&2
      exit 1
    fi
    BODY="$(printf '{"id":"%s","name":"%s","description":"%s","min_grade":"%s","capabilities":%s}' \
      "$ID" "$NAME" "$DESCRIPTION" "$MIN_GRADE" "$CAPABILITIES")"
    curl -sf \
      -X POST \
      -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
      -H "Content-Type: application/json" \
      -d "$BODY" \
      "${TELEGRAM_SERVER_URL}/admin/apps"
    ;;

  patch)
    if [ "$#" -lt 3 ]; then
      echo "Usage: manage.sh patch <id> <json_body>" >&2
      exit 1
    fi
    BODY="$3"
    curl -sf \
      -X PATCH \
      -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
      -H "Content-Type: application/json" \
      -d "$BODY" \
      "${TELEGRAM_SERVER_URL}/admin/apps/${ID}"
    ;;

  delete)
    curl -sf \
      -X DELETE \
      -H "Authorization: Bearer ${TELEGRAM_API_KEY}" \
      "${TELEGRAM_SERVER_URL}/admin/apps/${ID}"
    ;;

  *)
    echo "Unknown action: $ACTION. Must be one of: create, patch, delete" >&2
    exit 1
    ;;
esac
